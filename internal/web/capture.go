// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/scrape"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// Browser capture (docs/DESIGN.md §8.3): for sites that block server-side
// fetches, the user's own browser sends the page it already has.
//
//   - Desktop: a bookmarklet opens /capture in a new window and posts the
//     page to it with postMessage; /capture then submits it (same-origin).
//   - Phone: an iOS Shortcut POSTs the page to /api/v1/capture with a
//     capture-scoped token and opens the returned link to a short-lived
//     draft, which shows the usual "check and save" preview.

// maxCapture caps captured HTML (§8.3: 5 MiB), plus room for the form.
const maxCapture = 5<<20 + 64<<10

// draftTTL is how long a phone capture waits to be opened.
const draftTTL = time.Hour

// TokenStore is the API token storage the handlers need.
type TokenStore interface {
	CreateToken(ctx context.Context, userID, name string) (store.Token, string, error)
	Tokens(ctx context.Context, userID string) ([]store.Token, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	AuthenticateToken(ctx context.Context, secret, scope string) (recipe.User, error)
}

func (h *Handler) registerCapture(mux *http.ServeMux) {
	mux.HandleFunc("GET /capture", h.capturePage)
	mux.HandleFunc("POST /capture", h.captureSubmit)
	if h.opts.Tokens != nil {
		mux.HandleFunc("POST /api/v1/capture", h.captureAPI)
		mux.HandleFunc("GET /capture/d/{id}", h.captureDraft)
	}
}

func (h *Handler) capturePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, capturePage(""))
}

// captureSubmit receives the page posted by /capture's script (or by a
// plain form) and shows the preview. Nothing is saved yet.
func (h *Handler) captureSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCapture)
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusRequestEntityTooLarge, capturePage("That page is too large to capture (max 5 MB)."))
		return
	}
	res, pageURL, msg := h.extractCaptured(r.Context(), r.PostForm.Get("html"), r.PostForm.Get("url"))
	if msg != "" {
		h.render(w, r, http.StatusUnprocessableEntity, capturePage(msg))
		return
	}
	h.renderPreview(w, r, res, pageURL, recipe.SourceCapture)
}

// extractCaptured runs the same extraction as URL import (§8.2 steps 2–10)
// on HTML the browser supplied. The HTML is never stored (§8.3).
func (h *Handler) extractCaptured(ctx context.Context, html, rawURL string) (*scrape.Result, *url.URL, string) {
	pageURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (pageURL.Scheme != "http" && pageURL.Scheme != "https") || pageURL.Host == "" {
		return nil, nil, "The page address is missing or isn't a web address."
	}
	if strings.TrimSpace(html) == "" {
		return nil, nil, "No page was received."
	}
	res, err := scrape.Extract([]byte(html), pageURL)
	if err != nil {
		h.scraped(ScrapeError)
		return nil, nil, "No recipe found on that page. You can type it in instead."
	}
	h.scraped(string(res.Method))
	h.log.InfoContext(ctx, "capture", "domain", pageURL.Hostname(), "result", string(res.Method))
	return res, pageURL, ""
}

// --- phone capture API ---

type captureDraft struct {
	userID  string
	res     *scrape.Result
	pageURL *url.URL
	expires time.Time
}

// drafts holds phone captures until they are opened. One replica, so
// memory is enough; a restart just means capturing again.
type drafts struct {
	mu sync.Mutex
	m  map[string]captureDraft
}

func (d *drafts) put(c captureDraft) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]captureDraft{}
	}
	now := time.Now()
	for k, v := range d.m { // drop expired drafts
		if now.After(v.expires) {
			delete(d.m, k)
		}
	}
	if len(d.m) > 500 { // bounded even under abuse
		for k := range d.m {
			delete(d.m, k)
			break
		}
	}
	d.m[id] = c
	return id
}

func (d *drafts) get(id string) (captureDraft, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.m[id]
	if !ok || time.Now().After(c.expires) {
		return captureDraft{}, false
	}
	return c, true
}

// captureAPI is POST /api/v1/capture. It authenticates with a bearer
// token (the SSO proxy lets this one path through; see the deploy docs),
// extracts the recipe, and answers {"url": "<draft link>"} for the
// Shortcut to open.
func (h *Handler) captureAPI(w http.ResponseWriter, r *http.Request) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	u, err := h.opts.Tokens.AuthenticateToken(r.Context(), strings.TrimSpace(secret), "capture")
	if err != nil {
		if errors.Is(err, store.ErrBadToken) {
			writeJSONError(w, http.StatusUnauthorized, "invalid or revoked token")
			return
		}
		h.apiFail(w, r, err)
		return
	}
	if !h.limiter.allow("capture:"+u.ID, 30, time.Hour) {
		writeJSONError(w, http.StatusTooManyRequests, "too many captures; try again later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCapture)
	var in struct {
		URL  string `json:"url"`
		HTML string `json:"html"`
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "expected JSON {\"url\": …, \"html\": …} under 5 MB")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "expected form fields url and html under 5 MB")
			return
		}
		in.URL, in.HTML = r.PostForm.Get("url"), r.PostForm.Get("html")
	}
	res, pageURL, msg := h.extractCaptured(r.Context(), in.HTML, in.URL)
	if msg != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	id := h.drafts.put(captureDraft{userID: u.ID, res: res, pageURL: pageURL, expires: time.Now().Add(draftTTL)})
	writeJSON(w, http.StatusCreated, map[string]string{
		"url":   strings.TrimSuffix(h.opts.BaseURL, "/") + "/capture/d/" + id,
		"title": res.Recipe.Title,
	})
}

// captureDraft opens a phone capture's preview. Only the user whose token
// captured it can open it.
func (h *Handler) captureDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := h.drafts.get(r.PathValue("id"))
	if u, _ := httpx.UserFrom(r.Context()); !ok || u.ID != d.userID {
		h.render(w, r, http.StatusNotFound, capturePage("That capture has expired or isn't yours. Share the page again."))
		return
	}
	h.renderPreview(w, r, d.res, d.pageURL, recipe.SourceCapture)
}

// --- a small rate limiter (P5.5) ---

// limiter allows n events per window per key (fixed windows). It's for a
// household-sized app: a mutex and a map are plenty.
type limiter struct {
	mu sync.Mutex
	m  map[string]window
}

type window struct {
	start time.Time
	n     int
}

func (l *limiter) allow(key string, n int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]window{}
	}
	now := time.Now()
	w := l.m[key]
	if now.Sub(w.start) >= per {
		w = window{start: now}
	}
	if w.n >= n {
		return false
	}
	w.n++
	l.m[key] = w
	return true
}

// bookmarklet is the javascript: URL for the capture bookmarklet. It opens
// /capture, waits for it to say it's ready, then posts the page to it,
// addressed to our origin only.
func bookmarklet(base string) string {
	origin := base
	if u, err := url.Parse(base); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	js := `(()=>{const o=` + jsString(origin) + `;const w=window.open(o+'/capture','grimoire-capture');` +
		`if(!w){alert('Allow pop-ups for this site to save recipes to Kitchen Grimoire.');return}` +
		`const s=e=>{if(e.origin!==o||e.data!=='grimoire-ready')return;removeEventListener('message',s);` +
		`w.postMessage({type:'grimoire-capture',url:location.href,html:document.documentElement.outerHTML},o)};` +
		`addEventListener('message',s)})()`
	return "javascript:" + url.PathEscape(js)
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
