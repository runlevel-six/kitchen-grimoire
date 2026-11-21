// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/ingest"
)

// MaxBundleBytes caps an uploaded bundle zip. The legacy collection with
// photos is far smaller; a whole-library export with many phone photos
// fits comfortably.
const MaxBundleBytes = 512 << 20

// Bulk is the bulk import/export service behind the API (§8.4, FR-06).
// *ingest.Importer implements it.
type Bulk interface {
	ImportBundle(ctx context.Context, b *exportfmt.Bundle, opts ingest.Options) (ingest.Report, error)
	Export(ctx context.Context, w io.Writer) error
}

func (h *Handler) registerAPI(mux *http.ServeMux) {
	if h.opts.Bulk == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/import/bundle", h.importBundle)
	mux.HandleFunc("GET /api/v1/export", h.exportBundle)
}

// requireAdmin answers 403 unless the user is an admin (§12) and reports
// whether the handler may go on.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if u, ok := httpx.UserFrom(r.Context()); ok && u.IsAdmin {
		return true
	}
	writeJSONError(w, http.StatusForbidden, "admin only")
	return false
}

// importBundle is POST /api/v1/import/bundle[?dry_run=1]. The body is the
// bundle zip; the response is the JSON import report.
func (h *Handler) importBundle(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/zip" && mt != "application/x-zip-compressed" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "send the bundle as Content-Type: application/zip")
		return
	}

	// A zip is read from its end (the central directory), so it needs
	// random access: spool the body to a temp file rather than memory.
	tmp, err := os.CreateTemp("", "grimoire-bundle-*.zip")
	if err != nil {
		h.apiFail(w, r, err)
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	size, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, MaxBundleBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "bundle too large (max 512 MiB)")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "could not read the upload")
		return
	}

	bundle, err := exportfmt.ReadBundle(tmp, size, exportfmt.DefaultLimits())
	if errors.Is(err, exportfmt.ErrBundle) {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		h.apiFail(w, r, err)
		return
	}
	q := r.URL.Query().Get("dry_run")
	opts := ingest.Options{DryRun: q == "1" || q == "true", EditorID: userID(r)}
	report, err := h.opts.Bulk.ImportBundle(r.Context(), bundle, opts)
	if err != nil {
		h.apiFail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "bundle import", "dry_run", opts.DryRun, "created", report.Counts.Created,
		"updated", report.Counts.Updated, "unchanged", report.Counts.Unchanged,
		"skipped_conflict", report.Counts.SkippedConflict, "invalid", report.Counts.Invalid)
	writeJSON(w, http.StatusOK, report)
}

// exportBundle is GET /api/v1/export: a bundle of every live recipe.
func (h *Handler) exportBundle(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	name := "kitchen-grimoire-export-" + time.Now().UTC().Format("20060102") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	// The zip streams straight to the client. If something fails part-way
	// the status is already sent; the truncated zip won't open, and the
	// error is logged.
	if err := h.opts.Bulk.Export(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "export failed", "err", err)
	}
}

func (h *Handler) apiFail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "api request failed", "err", err, "path", r.URL.Path)
	writeJSONError(w, http.StatusInternalServerError, "the server hit an error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
