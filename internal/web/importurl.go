// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/safefetch"
	"github.com/runlevel-six/kitchen-grimoire/internal/scrape"
)

// Fetcher fetches pages and images for URL import (internal/safefetch).
type Fetcher interface {
	FetchPage(ctx context.Context, rawURL string) (*safefetch.Response, error)
	FetchImage(ctx context.Context, rawURL string) (*safefetch.Response, error)
}

// DuplicateFinder finds a live recipe already imported from a URL.
type DuplicateFinder interface {
	RecipeByCanonicalURL(ctx context.Context, canonical string) (*recipe.Summary, error)
}

// maxRawSource bounds the extracted JSON-LD carried through the preview
// form. Real Recipe objects are a few kilobytes.
const maxRawSource = 256 << 10

// Scrape results for the grimoire_scrape_total metric (§14.5).
const (
	ScrapeJSONLD    = "jsonld"
	ScrapeMicrodata = "microdata"
	ScrapeStub      = "opengraph_stub"
	ScrapeBlocked   = "blocked"
	ScrapeError     = "error"
)

func (h *Handler) registerImport(mux *http.ServeMux) {
	// Saving a checked preview serves both URL import and browser capture.
	mux.HandleFunc("POST /import/save", h.importSave)
	if h.opts.Fetcher == nil {
		return
	}
	mux.HandleFunc("GET /import", h.importForm)
	mux.HandleFunc("POST /import", h.importFetch)
}

func (h *Handler) importForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, importPage(r.URL.Query().Get("url"), ""))
}

func (h *Handler) scraped(result string) {
	if h.opts.OnScrape != nil {
		h.opts.OnScrape(result)
	}
}

// importFetch fetches and extracts the page, then shows the editor filled
// in. Nothing is saved until the user confirms (§8.2 step 9).
func (h *Handler) importFetch(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow("import-url:"+userID(r), 60, time.Hour) {
		h.render(w, r, http.StatusTooManyRequests, importPage("", "Too many imports in the last hour. Try again later."))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.PostForm.Get("url"))
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw // people paste "example.com/recipe"
	}
	pageURL, err := url.Parse(raw)
	if raw == "" || err != nil || (pageURL.Scheme != "http" && pageURL.Scheme != "https") || pageURL.Host == "" {
		h.render(w, r, http.StatusUnprocessableEntity, importPage(raw, "That doesn't look like a web address."))
		return
	}

	resp, err := h.opts.Fetcher.FetchPage(r.Context(), pageURL.String())
	if err != nil {
		result, msg := ScrapeError, "Couldn't load that page. Check the address and try again, or type the recipe in."
		var se *safefetch.StatusError
		switch {
		case errors.As(err, &se) && (se.StatusCode == 401 || se.StatusCode == 403 || se.StatusCode == 429 || se.StatusCode == 503):
			result = ScrapeBlocked
			msg = "That site refused our request (it blocks servers). Open the recipe on your phone and share it to Kitchen Grimoire, or type it in."
		case errors.Is(err, safefetch.ErrBlocked), errors.Is(err, safefetch.ErrPort), errors.Is(err, safefetch.ErrScheme):
			msg = "That address isn't allowed. Only public http(s) sites can be imported."
		case errors.Is(err, safefetch.ErrContentType):
			msg = "That address isn't a web page."
		case errors.Is(err, safefetch.ErrTimeout):
			msg = "That site took too long to answer. Try again in a moment."
		}
		h.scraped(result)
		h.log.InfoContext(r.Context(), "url import failed", "domain", pageURL.Hostname(), "result", result, "err", err)
		h.render(w, r, http.StatusOK, importPage(raw, msg))
		return
	}

	res, err := scrape.Extract(resp.Body, resp.FinalURL)
	if err != nil {
		h.scraped(ScrapeError)
		h.log.InfoContext(r.Context(), "url import found no recipe", "domain", pageURL.Hostname())
		h.render(w, r, http.StatusOK, importPage(raw, "No recipe found on that page. You can type it in instead."))
		return
	}
	h.scraped(string(res.Method))
	h.log.InfoContext(r.Context(), "url import", "domain", resp.FinalURL.Hostname(), "result", string(res.Method))
	h.renderPreview(w, r, res, resp.FinalURL, recipe.SourceURL)
}

// renderPreview shows an extracted recipe in the editor for checking. It is
// shared by URL import and browser capture.
func (h *Handler) renderPreview(w http.ResponseWriter, r *http.Request, res *scrape.Result, pageURL *url.URL, kind recipe.SourceKind) {
	f := formFromRecipe(res.Recipe)
	f.IsNew, f.Import = true, true
	f.SourceKind = string(kind)
	f.SourceURL = pageURL.String()
	f.CanonicalURL = res.CanonicalURL
	f.SourceSite = res.Recipe.SourceSite
	f.ReviewReasons = strings.Join(res.Recipe.ReviewReasons, ",")
	if len(res.RawSource) > 0 && len(res.RawSource) <= maxRawSource {
		f.RawSource = string(res.RawSource)
	}
	if len(res.ImageURLs) > 0 {
		f.ImageURL = res.ImageURLs[0]
	}
	if h.opts.Duplicates != nil && f.CanonicalURL != "" {
		if dup, err := h.opts.Duplicates.RecipeByCanonicalURL(r.Context(), f.CanonicalURL); err == nil && dup != nil {
			f.DuplicateSlug, f.DuplicateTitle = dup.Slug, dup.Title
		}
	}
	h.render(w, r, http.StatusOK, editorPage(f))
}

// importSave creates the recipe from the confirmed preview and then fetches
// its photo (best effort: a failed image never loses the recipe).
func (h *Handler) importSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	f, err := formFromRequest(r)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p := r.PostForm
	f.IsNew, f.Import = true, true
	f.CanonicalURL, f.SourceSite = p.Get("canonical_url"), p.Get("source_site")
	f.RawSource, f.ImageURL, f.ReviewReasons = p.Get("raw_source"), p.Get("image_url"), p.Get("review_reasons")
	f.SourceKind = p.Get("source_kind")
	if p.Get("action") != "save" && p.Get("action") != "" {
		h.render(w, r, http.StatusOK, editorPage(f)) // "+ group" round trip
		return
	}

	kind := recipe.SourceURL
	if f.SourceKind == string(recipe.SourceCapture) {
		kind = recipe.SourceCapture // only these two kinds can come from this form
	}
	rec := &recipe.Recipe{SourceKind: kind, CanonicalURL: f.CanonicalURL, SourceSite: f.SourceSite}
	if raw := []byte(f.RawSource); len(raw) > 0 && len(raw) <= maxRawSource && json.Valid(raw) {
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			rec.RawSource = compact.Bytes()
		}
	}
	for _, reason := range strings.Split(f.ReviewReasons, ",") {
		if reason = strings.TrimSpace(reason); reason != "" {
			rec.ReviewReasons = append(rec.ReviewReasons, reason)
		}
	}
	f.apply(rec)
	if len(f.Errors) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
		return
	}
	if err := h.svc.Create(r.Context(), rec, userID(r)); err != nil {
		var ve *recipe.ValidationError
		if errors.As(err, &ve) {
			f.Errors = ve.Fields
			h.render(w, r, http.StatusUnprocessableEntity, editorPage(f))
			return
		}
		h.fail(w, r, err)
		return
	}
	if f.ImageURL != "" && h.images.Files != nil && h.opts.Fetcher != nil && safeHTTPURL(f.ImageURL) {
		h.attachRemoteImage(r.Context(), rec, f.ImageURL)
	}
	redirect(w, r, "/r/"+rec.Slug+"?created=1")
}

// attachRemoteImage fetches, re-encodes, and attaches an image (§8.2 step 8,
// §8.5). Failures are logged, never shown: the recipe is already saved.
func (h *Handler) attachRemoteImage(ctx context.Context, rec *recipe.Recipe, rawURL string) {
	resp, err := h.opts.Fetcher.FetchImage(ctx, rawURL)
	if err != nil {
		h.log.InfoContext(ctx, "recipe image fetch failed", "recipe", rec.Slug, "err", err)
		return
	}
	stored, err := h.images.Files.Put(ctx, bytes.NewReader(resp.Body), safefetch.MaxImageBytes)
	if err != nil {
		// Unsupported formats (e.g. AVIF) keep only the source URL (§8.5).
		h.log.InfoContext(ctx, "recipe image not stored", "recipe", rec.Slug, "err", err)
		return
	}
	if _, err := h.images.Repo.AddImage(ctx, rec.ID, recipe.Image{
		SHA256: stored.SHA256, MIME: stored.MIME, Width: stored.Width, Height: stored.Height, SourceURL: rawURL,
	}); err != nil {
		h.log.WarnContext(ctx, "recipe image not recorded", "recipe", rec.Slug, "err", err)
	}
}
