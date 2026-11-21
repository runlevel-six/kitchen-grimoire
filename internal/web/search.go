// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// Searcher is the search and browse storage the handlers need.
type Searcher interface {
	Search(ctx context.Context, q store.SearchQuery) ([]recipe.Summary, int, error)
	CategoryTree(ctx context.Context) ([]store.CategoryCount, error)
	Contributors(ctx context.Context) ([]store.NameCount, error)
	Collections(ctx context.Context) ([]store.NameCount, error)
	Tags(ctx context.Context) ([]store.NameCount, error)
}

const searchPageSize = 40

// searchView is what the search template needs.
type searchView struct {
	Q        store.SearchQuery
	Title    string
	Results  []summaryT
	Total    int
	Page     int
	Pages    int
	Filters  []activeFilter
	BaseURL  string // path the form submits to
	ShowForm bool
}

type activeFilter struct {
	Label     string
	RemoveURL string
}

func (h *Handler) registerSearch(mux *http.ServeMux) {
	if h.search == nil {
		return
	}
	mux.HandleFunc("GET /search", h.searchPage)
	mux.HandleFunc("GET /browse", h.browseFacets)
	mux.HandleFunc("GET /browse/category/{path...}", h.browseCategory)
	mux.HandleFunc("GET /browse/contributor/{name}", h.browseContributor)
	mux.HandleFunc("GET /review", h.reviewQueue)
}

func queryFromURL(v url.Values) store.SearchQuery {
	page, _ := strconv.Atoi(v.Get("page"))
	page = max(page, 1)
	return store.SearchQuery{
		Text:        strings.TrimSpace(v.Get("q")),
		Category:    strings.Trim(v.Get("cat"), "/"),
		Tag:         v.Get("tag"),
		Contributor: strings.TrimSpace(v.Get("by")),
		Collection:  strings.TrimSpace(v.Get("col")),
		NeedsReview: v.Get("review") == "1",
		HasPhoto:    v.Get("photo") == "1",
		Limit:       searchPageSize,
		Offset:      (page - 1) * searchPageSize,
	}
}

func (h *Handler) runSearch(w http.ResponseWriter, r *http.Request, q store.SearchQuery, title string) {
	results, total, err := h.search.Search(r.Context(), q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := searchView{
		Q: q, Title: title, Results: results, Total: total,
		Page: q.Offset/searchPageSize + 1, Pages: max(1, (total+searchPageSize-1)/searchPageSize),
		BaseURL: "/search", ShowForm: true,
	}
	// Each active filter gets a chip that links to the same search without it.
	cur := r.URL.Query()
	if r.URL.Path != "/search" { // a browse/review URL: express its filter as query params
		cur = searchParams(q)
	}
	for _, f := range []struct{ key, label string }{
		{"cat", "Category: " + strings.ReplaceAll(q.Category, "/", " › ")},
		{"tag", "Tag: " + q.Tag},
		{"by", "From: " + q.Contributor},
		{"col", "Collection: " + q.Collection},
		{"review", "Needs review"},
		{"photo", "Has a photo"},
	} {
		if cur.Get(f.key) == "" || cur.Get(f.key) == "0" {
			continue
		}
		rest := url.Values{}
		for k, vals := range cur {
			if k != f.key && k != "page" {
				rest[k] = vals
			}
		}
		v.Filters = append(v.Filters, activeFilter{Label: f.label, RemoveURL: "/search?" + rest.Encode()})
	}
	h.render(w, r, http.StatusOK, searchPage(v, cur))
}

// searchParams renders a query as /search parameters.
func searchParams(q store.SearchQuery) url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("q", q.Text)
	set("cat", q.Category)
	set("tag", q.Tag)
	set("by", q.Contributor)
	set("col", q.Collection)
	if q.NeedsReview {
		v.Set("review", "1")
	}
	if q.HasPhoto {
		v.Set("photo", "1")
	}
	return v
}

func (h *Handler) searchPage(w http.ResponseWriter, r *http.Request) {
	q := queryFromURL(r.URL.Query())
	title := "All recipes"
	if q.Text != "" {
		title = "Results for “" + q.Text + "”"
	}
	h.runSearch(w, r, q, title)
}

func (h *Handler) browseCategory(w http.ResponseWriter, r *http.Request) {
	q := queryFromURL(r.URL.Query())
	q.Category = strings.Trim(r.PathValue("path"), "/")
	h.runSearch(w, r, q, "Category: "+strings.ReplaceAll(q.Category, "/", " › "))
}

func (h *Handler) browseContributor(w http.ResponseWriter, r *http.Request) {
	q := queryFromURL(r.URL.Query())
	q.Contributor = r.PathValue("name")
	h.runSearch(w, r, q, "From "+q.Contributor)
}

func (h *Handler) reviewQueue(w http.ResponseWriter, r *http.Request) {
	q := queryFromURL(r.URL.Query())
	q.NeedsReview = true
	h.runSearch(w, r, q, "Review queue")
}

// facets is what the browse page shows.
type facets struct {
	Categories  []store.CategoryCount
	People      []store.NameCount
	Collections []store.NameCount
	Tags        []store.NameCount
	Total       int
}

func (h *Handler) browseFacets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var f facets
	var err error
	if f.Categories, err = h.search.CategoryTree(ctx); err != nil {
		h.fail(w, r, err)
		return
	}
	if f.People, err = h.search.Contributors(ctx); err != nil {
		h.fail(w, r, err)
		return
	}
	if f.Collections, err = h.search.Collections(ctx); err != nil {
		h.fail(w, r, err)
		return
	}
	if f.Tags, err = h.search.Tags(ctx); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, f.Total, err = h.svc.List(ctx, recipe.ListOptions{Limit: 1}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, browseFacetsPage(f))
}

func pageURL(cur url.Values, page int) string {
	v := url.Values{}
	for k, vals := range cur {
		v[k] = vals
	}
	v.Set("page", strconv.Itoa(page))
	return "/search?" + v.Encode()
}

func contributorURL(name string) string { return "/browse/contributor/" + url.PathEscape(name) }

func collectionURL(name string) string { return "/search?col=" + url.QueryEscape(name) }

func tagURL(slug string) string { return "/search?tag=" + url.QueryEscape(slug) }
