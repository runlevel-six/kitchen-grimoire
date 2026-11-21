// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/safefetch"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// fakeSite serves an invented recipe page, its photo, a blocked page, and a
// page with no recipe.
func fakeSite(t *testing.T) *httptest.Server {
	png := pngBytes(t, 640, 480, color.RGBA{180, 90, 30, 255})
	mux := http.NewServeMux()
	mux.HandleFunc("/pancakes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<!doctype html><html><head><title>Example Pancakes</title>
<link rel="canonical" href="/pancakes?utm_source=feed">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Recipe",
"name":"Example Pancakes","author":{"@type":"Person","name":"Example Test Kitchen"},
"recipeYield":"8 pancakes","totalTime":"PT25M","image":"/pancakes.png",
"recipeIngredient":["1 ½ cups flour","2 eggs","1 cup milk"],
"recipeInstructions":[{"@type":"HowToStep","text":"Whisk everything."},{"@type":"HowToStep","text":"Cook 2-3 minutes per side."}]}
</script></head><body></body></html>`)
	})
	mux.HandleFunc("/pancakes.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	})
	mux.HandleFunc("/blocked", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no bots", http.StatusForbidden)
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><body>Just a blog post.</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newImportEnv(t *testing.T) (*env, *[]string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenDataDir(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	svc := recipe.NewService(st)
	u, _ := st.UpsertUser(ctx, "tester", "Test User", "")
	files, _ := imagestore.New(t.TempDir())
	var outcomes []string
	fetcher := safefetch.New(safefetch.Config{AllowHosts: []string{"127.0.0.1"}}) // test-only allowlist
	mux := http.NewServeMux()
	New(slog.New(slog.NewTextHandler(io.Discard, nil)), svc, Images{Files: files, Repo: st}, Options{
		Search: st, Fetcher: fetcher, Duplicates: st,
		OnScrape: func(r string) { outcomes = append(outcomes, r) },
	}).Register(mux)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(httpx.WithUser(r.Context(), u)))
	})
	return &env{h: httpx.SecurityHeaders(h), svc: svc, st: st}, &outcomes
}

// formValues pulls the named inputs and textareas out of a rendered form.
func formValues(body string) url.Values {
	v := url.Values{}
	for _, m := range regexp.MustCompile(`<input[^>]*name="([^"]+)"[^>]*value="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		v.Add(m[1], htmlUnescape(m[2]))
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea[^>]*name="([^"]+)"[^>]*>(.*?)</textarea>`).FindAllStringSubmatch(body, -1) {
		v.Add(m[1], htmlUnescape(m[2]))
	}
	return v
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`).Replace(s)
}

func TestURLImport(t *testing.T) {
	site := fakeSite(t)
	e, outcomes := newImportEnv(t)

	res, body := e.get(t, "/import")
	wantStatus(t, res, 200, body)

	res, body = e.post(t, "/import", url.Values{"url": {site.URL + "/pancakes"}})
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Check and save", "Example Pancakes", "1 ½ cups flour", "Whisk everything.",
		"Example Test Kitchen", `name="raw_source"`, "photo will be saved")

	form := formValues(body)
	form.Set("action", "save")
	if form.Get("canonical_url") != site.URL+"/pancakes" {
		t.Errorf("canonical_url = %q (tracking params must be stripped)", form.Get("canonical_url"))
	}
	res, body = e.post(t, "/import/save", form)
	wantStatus(t, res, http.StatusSeeOther, body)

	rec, err := e.svc.Get(context.Background(), "example-pancakes")
	if err != nil {
		t.Fatal(err)
	}
	if rec.SourceKind != recipe.SourceURL || rec.SourceURL != site.URL+"/pancakes" || len(rec.RawSource) == 0 ||
		rec.TotalMin != 25 || rec.IngredientCount() != 3 || rec.StepCount() != 2 || len(rec.Images) != 1 {
		t.Fatalf("imported recipe = %+v", rec)
	}
	if !rec.IngredientGroups[0].Ingredients[0].ParseOK {
		t.Error("ingredient lines were not parsed on save")
	}

	// Importing the same page again warns about the duplicate.
	_, body = e.post(t, "/import", url.Values{"url": {site.URL + "/pancakes"}})
	wantContains(t, body, "already in your library", `href="/r/example-pancakes"`)

	_, body = e.post(t, "/import", url.Values{"url": {site.URL + "/blocked"}})
	wantContains(t, body, "refused our request")
	_, body = e.post(t, "/import", url.Values{"url": {site.URL + "/plain"}})
	wantContains(t, body, "No recipe found") // not even a title: nothing to pre-fill
	_, body = e.post(t, "/import", url.Values{"url": {"not a url at all"}})
	wantContains(t, body, "doesn&#39;t look like a web address")
	_, body = e.post(t, "/import", url.Values{"url": {"http://169.254.169.254/latest/meta-data"}})
	wantContains(t, body, "isn&#39;t allowed")

	got := strings.Join(*outcomes, ",")
	if !strings.HasPrefix(got, "jsonld,jsonld,blocked,error") {
		t.Errorf("scrape outcomes = %s", got)
	}
}
