// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/ingest"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// settingsView is what the settings page shows.
type settingsView struct {
	IsAdmin     bool
	Tokens      []store.Token
	NewToken    string // shown once, right after creation
	NewTokenFor string
	Bookmarklet string
	CaptureURL  string
	Report      *ingest.Report
	ImportError string
	Message     string
}

func (h *Handler) registerSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.settingsPage)
	if h.opts.Tokens != nil {
		mux.HandleFunc("POST /settings/tokens", h.createToken)
		mux.HandleFunc("POST /settings/tokens/{id}/revoke", h.revokeToken)
	}
	if h.opts.Bulk != nil {
		mux.HandleFunc("POST /settings/import", h.settingsImport)
	}
}

func (h *Handler) settingsView(r *http.Request) (settingsView, error) {
	u, _ := currentUser(r.Context())
	v := settingsView{
		IsAdmin:     u.IsAdmin,
		Bookmarklet: bookmarklet(h.opts.BaseURL),
		CaptureURL:  strings.TrimSuffix(h.opts.BaseURL, "/") + "/api/v1/capture",
	}
	if h.opts.Tokens != nil {
		owner := u.ID
		if u.IsAdmin {
			owner = "" // admins see and can revoke everyone's tokens
		}
		var err error
		if v.Tokens, err = h.opts.Tokens.Tokens(r.Context(), owner); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	v, err := h.settingsView(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if r.URL.Query().Get("revoked") != "" {
		v.Message = "Token revoked."
	}
	h.render(w, r, http.StatusOK, settingsPage(v))
}

// createToken makes a capture token and shows it once. The secret is in
// this response only: never in a URL, a log, or the database.
func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" {
		name = "Phone"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	u, _ := currentUser(r.Context())
	if !h.limiter.allow("token:"+u.ID, 20, time.Hour) {
		http.Error(w, "too many tokens created; try again later", http.StatusTooManyRequests)
		return
	}
	_, secret, err := h.opts.Tokens.CreateToken(r.Context(), u.ID, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "api token created", "user", u.RemoteUser, "name", name)
	v, err := h.settingsView(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.NewToken, v.NewTokenFor = secret, name
	h.render(w, r, http.StatusOK, settingsPage(v))
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	owner := u.ID
	if u.IsAdmin {
		owner = ""
	}
	err := h.opts.Tokens.RevokeToken(r.Context(), owner, r.PathValue("id"))
	if err != nil && !errors.Is(err, store.ErrBadToken) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/settings?revoked=1")
}

// settingsImport is the admin bundle upload (P5.4): the same import as
// POST /api/v1/import/bundle, from a form, with the report shown as a page.
func (h *Handler) settingsImport(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	if !u.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	v, err := h.settingsView(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.limiter.allow("import:"+u.ID, 20, time.Hour) {
		v.ImportError = "Too many imports in the last hour; try again later."
		h.render(w, r, http.StatusTooManyRequests, settingsPage(v))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBundleBytes+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		v.ImportError = "Choose a bundle file to upload."
		h.render(w, r, http.StatusBadRequest, settingsPage(v))
		return
	}
	// Spool the zip to a temp file: zips are read from the end.
	tmp, err := os.CreateTemp("", "grimoire-bundle-*.zip")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	var size int64
	dryRun := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			v.ImportError = "The upload was interrupted or is larger than 512 MB."
			h.render(w, r, http.StatusBadRequest, settingsPage(v))
			return
		}
		switch part.FormName() {
		case "dry_run":
			b, _ := io.ReadAll(io.LimitReader(part, 8))
			dryRun = string(b) == "1"
		case "bundle":
			if size, err = io.Copy(tmp, part); err != nil {
				v.ImportError = "The upload was interrupted or is larger than 512 MB."
				h.render(w, r, http.StatusBadRequest, settingsPage(v))
				return
			}
		}
	}
	bundle, err := exportfmt.ReadBundle(tmp, size, exportfmt.DefaultLimits())
	if err != nil {
		v.ImportError = "That isn't a valid bundle: " + err.Error()
		h.render(w, r, http.StatusUnprocessableEntity, settingsPage(v))
		return
	}
	report, err := h.opts.Bulk.ImportBundle(r.Context(), bundle, ingest.Options{DryRun: dryRun, EditorID: u.ID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "bundle import (settings)", "dry_run", dryRun, "created", report.Counts.Created,
		"updated", report.Counts.Updated, "skipped_conflict", report.Counts.SkippedConflict, "invalid", report.Counts.Invalid)
	v.Report = &report
	h.render(w, r, http.StatusOK, settingsPage(v))
}
