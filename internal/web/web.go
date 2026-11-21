// SPDX-License-Identifier: Apache-2.0

// Package web holds the HTTP handlers and templ components for the pages.
package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
	assets "github.com/runlevel-six/kitchen-grimoire/web"
)

// Short aliases used by the templates.
type (
	recipeT   = recipe.Recipe
	summaryT  = recipe.Summary
	revisionT = recipe.RevisionInfo
)

// scaleChoices are the scaler's buttons. Big, few, and easy to hit with a
// floury thumb beat a free-form number field in the kitchen.
var scaleChoices = []float64{0.5, 1, 2, 3}

// Options configures the handlers.
type Options struct {
	TrashDays int
	// Search enables search, browse, and the review queue.
	Search Searcher
	// Fetcher enables URL import; Duplicates warns about re-imports;
	// OnScrape counts import outcomes for metrics.
	Fetcher    Fetcher
	Duplicates DuplicateFinder
	OnScrape   func(result string)
	// Favorites enables per-user favorites (FR-13).
	Favorites FavoriteStore
	// Bulk enables the admin bundle import and export API.
	Bulk Bulk
	// Tokens enables capture tokens and the phone capture API (§8.3).
	Tokens TokenStore
	// BaseURL is the public address, for links the server hands out.
	BaseURL string
	// Reviewer clears review flags (FR-12).
	Reviewer interface {
		MarkReviewed(ctx context.Context, id string) error
	}
}

// FavoriteStore is the favorites storage the handlers need.
type FavoriteStore interface {
	SetFavorite(ctx context.Context, userID, recipeID string, on bool) error
	IsFavorite(ctx context.Context, userID, recipeID string) (bool, error)
	Favorites(ctx context.Context, userID string, limit int) ([]recipe.Summary, error)
}

// Handler serves the pages.
type Handler struct {
	log     *slog.Logger
	svc     *recipe.Service
	images  Images
	search  Searcher
	opts    Options
	drafts  *drafts
	limiter *limiter
}

// New returns a Handler.
// images may be the zero value, which disables photo routes.
func New(log *slog.Logger, svc *recipe.Service, images Images, opts Options) *Handler {
	if opts.TrashDays <= 0 {
		opts.TrashDays = 30
	}
	return &Handler{log: log, svc: svc, images: images, search: opts.Search, opts: opts,
		drafts: &drafts{}, limiter: &limiter{}}
}

// Register adds the page routes and static assets to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /new", h.newForm)
	mux.HandleFunc("POST /new", h.create)
	mux.HandleFunc("GET /r/{slug}", h.view)
	mux.HandleFunc("POST /r/{slug}", h.update)
	mux.HandleFunc("GET /r/{slug}/edit", h.editForm)
	mux.HandleFunc("GET /r/{slug}/cook", h.cook)
	mux.HandleFunc("POST /r/{slug}/delete", h.delete)
	mux.HandleFunc("POST /r/{slug}/undelete", h.undelete)
	mux.HandleFunc("GET /r/{slug}/history", h.history)
	mux.HandleFunc("GET /r/{slug}/history/{rev}", h.revision)
	mux.HandleFunc("POST /r/{slug}/history/{rev}/restore", h.restoreRevision)
	mux.HandleFunc("GET /trash", h.trash)
	mux.HandleFunc("POST /trash/empty", h.emptyTrash)
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /sw.js", serviceWorker)
	h.registerImages(mux)
	h.registerSearch(mux)
	h.registerImport(mux)
	if h.opts.Favorites != nil {
		mux.HandleFunc("POST /r/{slug}/favorite", h.toggleFavorite)
	}
	h.registerAPI(mux)
	if h.opts.Reviewer != nil {
		mux.HandleFunc("POST /r/{slug}/reviewed", h.markReviewed)
	}
	h.registerCapture(mux)
	h.registerSettings(mux)
}

// --- rendering and errors ---

type ctxKey int

const trashDaysKey ctxKey = 1

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The scaler swaps fragments of the same URL; caches must key on it.
	w.Header().Set("Vary", "HX-Request")
	w.Header().Set("Cache-Control", "no-store") // pages are per-user and change often
	w.WriteHeader(status)
	ctx := context.WithValue(r.Context(), trashDaysKey, h.opts.TrashDays)
	if err := c.Render(ctx, w); err != nil {
		h.log.ErrorContext(r.Context(), "render failed", "err", err, "path", r.URL.Path)
	}
}

// fail renders the right page for an error from the service.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, recipe.ErrNotFound):
		h.render(w, r, http.StatusNotFound, notFoundPage())
	case errors.Is(err, recipe.ErrDeleted):
		h.render(w, r, http.StatusConflict, errorPage("That recipe is in the trash. Restore it first."))
	default:
		h.log.ErrorContext(r.Context(), "request failed", "err", err, "path", r.URL.Path)
		h.render(w, r, http.StatusInternalServerError, errorPage("The server hit an error. Please try again."))
	}
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func currentUser(ctx context.Context) (recipe.User, bool) { return httpx.UserFrom(ctx) }

func userID(r *http.Request) string {
	u, _ := httpx.UserFrom(r.Context())
	return u.ID
}

func trashDays(ctx context.Context) int {
	if d, ok := ctx.Value(trashDaysKey).(int); ok {
		return d
	}
	return 30
}

// --- flash messages (carried in the redirect's query string) ---

type flash struct {
	Kind       string // "ok" or "warn"
	Text       string
	UndoAction string // POST target for an Undo button
}

func flashFrom(r *http.Request) flash {
	q := r.URL.Query()
	switch {
	case q.Get("created") != "":
		return flash{Kind: "ok", Text: "Recipe added."}
	case q.Get("saved") != "":
		return flash{Kind: "ok", Text: "Saved."}
	case q.Get("restored") != "":
		return flash{Kind: "ok", Text: "Restored."}
	case q.Get("deleted") != "":
		slug := recipe.Slugify(q.Get("deleted")) // only ever a slug; never echo raw input into a URL
		return flash{Kind: "ok", Text: "Moved to the trash.", UndoAction: "/r/" + slug + "/undelete"}
	case q.Get("emptied") != "":
		n, _ := strconv.Atoi(q.Get("emptied"))
		return flash{Kind: "ok", Text: "Trash emptied: " + plural(n, "recipe", "recipes") + " removed."}
	}
	return flash{}
}

// --- pages ---

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	recent, total, err := h.svc.List(r.Context(), recipe.ListOptions{Order: "created", Limit: 12})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	updated, _, err := h.svc.List(r.Context(), recipe.ListOptions{Order: "updated", Limit: 8})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// Don't repeat what's already under "Recently added".
	seen := map[string]bool{}
	for _, s := range recent {
		seen[s.ID] = true
	}
	updated = slices.DeleteFunc(updated, func(s recipe.Summary) bool { return seen[s.ID] })
	var favs []recipe.Summary
	if h.opts.Favorites != nil {
		if favs, err = h.opts.Favorites.Favorites(r.Context(), userID(r), 12); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.render(w, r, http.StatusOK, homePage(favs, recent, updated, total, flashFrom(r)))
}

func (h *Handler) view(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, recipePage(rec, parseFactor(r.URL.Query().Get("x")), h.isFavorite(r, rec), flashFrom(r)))
}

func (h *Handler) cook(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	steps := rec.FlatSteps()
	if len(steps) == 0 {
		redirect(w, r, "/r/"+rec.Slug)
		return
	}
	cur, _ := strconv.Atoi(r.URL.Query().Get("step"))
	cur = max(1, min(cur, len(steps)))
	h.render(w, r, http.StatusOK, cookPage(rec, steps, cur))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), rec.ID); err != nil && !errors.Is(err, recipe.ErrDeleted) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/?deleted="+url.QueryEscape(rec.Slug))
}

func (h *Handler) undelete(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Restore(r.Context(), rec.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/r/"+rec.Slug+"?restored=1")
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	revs, err := h.svc.History(r.Context(), rec.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, historyPage(rec, revs))
}

func (h *Handler) revision(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil {
		h.render(w, r, http.StatusNotFound, notFoundPage())
		return
	}
	old, err := h.svc.Revision(r.Context(), rec.ID, n)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, revisionPage(rec, old, n))
}

func (h *Handler) restoreRevision(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil {
		h.render(w, r, http.StatusNotFound, notFoundPage())
		return
	}
	if _, err := h.svc.RestoreRevision(r.Context(), rec.ID, n, userID(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/r/"+rec.Slug+"?restored=1")
}

func (h *Handler) trash(w http.ResponseWriter, r *http.Request) {
	list, total, err := h.svc.List(r.Context(), recipe.ListOptions{Deleted: true, Order: "deleted", Limit: 500})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, _ := currentUser(r.Context())
	h.render(w, r, http.StatusOK, trashPage(list, total, h.opts.TrashDays, u.IsAdmin, flashFrom(r)))
}

// emptyTrash permanently deletes everything in the trash. Admin only, and
// it needs the explicit confirmation checkbox (§11.1: the one place we ask).
func (h *Handler) emptyTrash(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	if !u.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("confirm") != "yes" {
		redirect(w, r, "/trash")
		return
	}
	n, err := h.svc.EmptyTrash(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "trash emptied", "recipes", n, "by", u.RemoteUser)
	redirect(w, r, "/trash?emptied="+strconv.Itoa(n))
}

// --- view helpers ---

// parseFactor accepts only the scaler's own choices; anything else is 1×.
func parseFactor(s string) float64 {
	if s == "½" {
		return 0.5
	}
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

// safeHTTPURL guards links built from stored data: only http(s).
func safeHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func stepURL(slug string, n int) string {
	return "/r/" + slug + "/cook?step=" + strconv.Itoa(max(n, 1))
}

func slugify(s string) string { return recipe.Slugify(s) }

func categoryPathURL(path []string) string { return recipe.CategoryPathSlug(path) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func cardMeta(s summaryT) string {
	var parts []string
	if s.ContributedBy != "" {
		parts = append(parts, "From "+s.ContributedBy)
	}
	if s.YieldText != "" {
		parts = append(parts, s.YieldText)
	}
	if s.TotalMin > 0 {
		parts = append(parts, minutes(s.TotalMin))
	}
	if s.NeedsReview {
		parts = append(parts, "needs review")
	}
	return strings.Join(parts, " · ")
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute", "minutes") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour", "hours") + " ago"
	default:
		return plural(int(d.Hours()/24), "day", "days") + " ago"
	}
}

func deletedAgo(s summaryT) string {
	if s.DeletedAt == nil {
		return ""
	}
	return "Deleted " + ago(*s.DeletedAt)
}

func revisionMeta(v revisionT) string {
	s := v.EditedAt.Local().Format("Jan 2, 2006 3:04 PM")
	if v.EditedBy != "" {
		s += " by " + v.EditedBy
	}
	return s
}

// --- static assets ---

var (
	staticFS    = mustSub(assets.Static, "static")
	hashOnce    sync.Once
	assetHashes map[string]string
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
		assetHashes = map[string]string{}
		_ = fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(staticFS, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			assetHashes[p] = hex.EncodeToString(sum[:])[:12]
			return nil
		})
	})
	return "/static/" + name + "?v=" + assetHashes[name]
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
		// The standard library's MIME table has no entry for .webmanifest.
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}

func (h *Handler) isFavorite(r *http.Request, rec *recipe.Recipe) bool {
	if h.opts.Favorites == nil {
		return false
	}
	on, err := h.opts.Favorites.IsFavorite(r.Context(), userID(r), rec.ID)
	if err != nil {
		h.log.WarnContext(r.Context(), "favorite lookup failed", "err", err)
	}
	return on
}

// toggleFavorite sets or clears a favorite. htmx swaps just the button;
// without JavaScript the form posts and we redirect back.
func (h *Handler) toggleFavorite(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	_ = r.ParseForm()
	on := r.PostForm.Get("on") == "1"
	if err := h.opts.Favorites.SetFavorite(r.Context(), userID(r), rec.ID, on); err != nil {
		h.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.render(w, r, http.StatusOK, favoriteButton(rec.Slug, on))
		return
	}
	redirect(w, r, "/r/"+rec.Slug)
}

// serviceWorker serves the PWA service worker from the site root so it can
// control every page (a worker's scope can't be wider than its path).
func serviceWorker(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(staticFS, "js/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache") // browsers must see updates promptly
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write(b)
}

// markReviewed clears the review flag and goes back to the queue.
func (h *Handler) markReviewed(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.opts.Reviewer.MarkReviewed(r.Context(), rec.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/review")
}

func tokenMeta(t store.Token, showOwner bool) string {
	s := "created " + t.CreatedAt.Local().Format("Jan 2, 2006")
	if t.LastUsedAt != nil {
		s += ", last used " + ago(*t.LastUsedAt)
	} else {
		s += ", never used"
	}
	if showOwner {
		s = t.UserName + " · " + s
	}
	return s
}

func joinReasons(reasons, warnings []string) string {
	return strings.Join(append(append([]string{}, reasons...), warnings...), "; ")
}
