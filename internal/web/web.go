// SPDX-License-Identifier: Apache-2.0

// Package web holds the HTTP handlers and templ components for the pages.
package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	assets "github.com/runlevel-six/kitchen-grimoire/web"
)

// recipeT is a short alias used by the templates.
type recipeT = recipe.Recipe

// scaleChoices are the scaler's buttons. Big, few, and easy to hit with a
// floury thumb beat a free-form number field in the kitchen.
var scaleChoices = []float64{0.5, 1, 2, 3}

// Handler serves the pages.
type Handler struct {
	log     *slog.Logger
	recipes []*recipe.Recipe
	bySlug  map[string]*recipe.Recipe
}

// New returns a Handler over the given recipes. (P1 replaces the slice with
// the store.)
func New(log *slog.Logger, recipes []*recipe.Recipe) *Handler {
	h := &Handler{log: log, recipes: recipes, bySlug: map[string]*recipe.Recipe{}}
	for _, r := range recipes {
		h.bySlug[r.Slug] = r
	}
	return h
}

// Register adds the page routes and static assets to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /r/{slug}", h.recipe)
	mux.HandleFunc("GET /r/{slug}/cook", h.cook)
	mux.Handle("GET /static/", staticHandler())
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, homePage(h.recipes))
}

func (h *Handler) recipe(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.bySlug[r.PathValue("slug")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.render(w, r, recipePage(rec, parseFactor(r.URL.Query().Get("x"))))
}

func (h *Handler) cook(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.bySlug[r.PathValue("slug")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	steps := rec.FlatSteps()
	cur, _ := strconv.Atoi(r.URL.Query().Get("step"))
	cur = max(1, min(cur, len(steps)))
	h.render(w, r, cookPage(rec, steps, cur))
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The scaler swaps fragments of the same URL, so caches must key on the
	// htmx header as well (it changes nothing today, but keeps intermediaries honest).
	w.Header().Set("Vary", "HX-Request")
	if err := c.Render(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "render failed", "err", err, "path", r.URL.Path)
	}
}

// parseFactor accepts only the scaler's own choices; anything else is 1×.
func parseFactor(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !slices.Contains(scaleChoices, f) {
		return 1
	}
	return f
}

func fmtFactor(f float64) string {
	if f == 0.5 {
		return "½"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func yieldText(r *recipeT, factor float64) string {
	if factor == 1 || r.Servings == 0 {
		return r.YieldText
	}
	return "Makes " + strconv.FormatFloat(r.Servings*factor, 'f', -1, 64) +
		" (" + fmtFactor(factor) + "× " + r.YieldText + ")"
}

func minutes(m int) string {
	if m < 60 {
		return strconv.Itoa(m) + " min"
	}
	s := strconv.Itoa(m/60) + " hr"
	if m%60 != 0 {
		s += " " + strconv.Itoa(m%60) + " min"
	}
	return s
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func stepURL(slug string, n int) string {
	return "/r/" + slug + "/cook?step=" + strconv.Itoa(max(n, 1))
}

// --- static assets ---

var (
	staticFS   = mustSub(assets.Static, "static")
	hashOnce   sync.Once
	assetHashs map[string]string
)

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// asset returns the URL for a static file with a content hash in the query
// string, so the file can be cached forever and still update on deploy.
func asset(name string) string {
	hashOnce.Do(func() {
		assetHashs = map[string]string{}
		_ = fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(staticFS, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			assetHashs[p] = hex.EncodeToString(sum[:])[:12]
			return nil
		})
	})
	return "/static/" + name + "?v=" + assetHashs[name]
}

func staticHandler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		files.ServeHTTP(w, r)
	})
}
