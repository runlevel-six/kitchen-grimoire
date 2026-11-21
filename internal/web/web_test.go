// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/json"
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
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

type env struct {
	h   http.Handler
	svc *recipe.Service
	st  *store.Store
}

// newEnv wires the real stack (SQLite, service, handlers) with a fixed
// logged-in user, as the auth middleware would.
func newEnv(t *testing.T, admin bool) *env {
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
	u, err := st.UpsertUser(ctx, "tester", "Test User", "")
	if err != nil {
		t.Fatal(err)
	}
	u.IsAdmin = admin
	files, err := imagestore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(slog.New(slog.NewTextHandler(io.Discard, nil)), svc, Images{Files: files, Repo: st}, Options{TrashDays: 30, Search: st}).Register(mux)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(httpx.WithUser(r.Context(), u)))
	})
	return &env{h: httpx.SecurityHeaders(h), svc: svc, st: st}
}

func (e *env) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(b)
}

func (e *env) post(t *testing.T, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(b)
}

func wantStatus(t *testing.T, res *http.Response, code int, body string) {
	t.Helper()
	if res.StatusCode != code {
		t.Fatalf("status = %d, want %d; body: %.400s", res.StatusCode, code, body)
	}
}

func wantContains(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

func lemonForm() url.Values {
	return url.Values{
		"action":         {"save"},
		"title":          {"Example Lemon Cream Bars"},
		"yield_text":     {"24 bars"},
		"servings":       {"24"},
		"prep_min":       {"25"},
		"ig_name":        {"Crust", "Filling"},
		"ig_lines":       {"2 cups all-purpose flour\npinch of salt", "1 8-oz pkg cream cheese, softened\n3-4 Tbsp lemon juice"},
		"sg_name":        {""},
		"sg_text":        {"Preheat the oven.\n\nBake 18-20 minutes."},
		"contributed_by": {"Aunt Example"},
		"collection":     {"Example Family Cookbook"},
		"categories":     {"Desserts > Cookies; Fruits"},
		"tags":           {"party, make-ahead"},
	}
}

func TestCreateViewEdit(t *testing.T) {
	e := newEnv(t, false)

	res, body := e.get(t, "/")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "No recipes yet")

	res, body = e.get(t, "/new")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "New recipe", `name="ig_lines"`)

	res, body = e.post(t, "/new", lemonForm())
	wantStatus(t, res, http.StatusSeeOther, body)
	loc := res.Header.Get("Location")
	if loc != "/r/example-lemon-cream-bars?created=1" {
		t.Fatalf("redirect = %q", loc)
	}

	res, body = e.get(t, loc)
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Recipe added.", "From Aunt Example · Example Family Cookbook",
		"1 8-oz pkg cream cheese, softened", "Desserts › Cookies", "#party", "Start cooking", "History (1 revision)")

	res, body = e.get(t, "/r/example-lemon-cream-bars?x=2")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "2 8-oz pkg cream cheese, softened", "6–8 Tbsp lemon juice", "not scaled", "Makes 48")

	// Edit form shows the stored text and flags the unparsed line.
	res, body = e.get(t, "/r/example-lemon-cream-bars/edit")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Edit: Example Lemon Cream Bars", "pinch of salt", `class="unparsed"`,
		"Desserts &gt; Cookies; Fruits", `name="revision" value="1"`)

	f := lemonForm()
	f.Set("title", "Example Lemon Cream Bars (Less Sugar)")
	f.Set("revision", "1")
	res, body = e.post(t, "/r/example-lemon-cream-bars", f)
	wantStatus(t, res, http.StatusSeeOther, body)
	// The slug stays the same after a title change.
	if loc := res.Header.Get("Location"); loc != "/r/example-lemon-cream-bars?saved=1" {
		t.Errorf("redirect after edit = %q (slug must not change)", loc)
	}
	_, body = e.get(t, res.Header.Get("Location"))
	wantContains(t, body, "Saved.", "Less Sugar", "History (2 revisions)")
}

func TestEditConflictKeepsUserText(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())

	first := lemonForm()
	first.Set("revision", "1")
	first.Set("notes", "first edit")
	if res, body := e.post(t, "/r/example-lemon-cream-bars", first); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("first edit: %d %s", res.StatusCode, body)
	}

	second := lemonForm()
	second.Set("revision", "1") // stale
	second.Set("notes", "my competing edit")
	res, body := e.post(t, "/r/example-lemon-cream-bars", second)
	wantStatus(t, res, http.StatusConflict, body)
	wantContains(t, body, "Someone else saved this recipe", "my competing edit", `name="revision" value="2"`)

	// Saving again from the conflict form overwrites deliberately.
	second.Set("revision", "2")
	res, body = e.post(t, "/r/example-lemon-cream-bars", second)
	wantStatus(t, res, http.StatusSeeOther, body)
	rec, _ := e.svc.Get(context.Background(), "example-lemon-cream-bars")
	if rec.Notes != "my competing edit" || rec.Revision != 3 {
		t.Errorf("after overwrite: notes=%q rev=%d", rec.Notes, rec.Revision)
	}
}

func TestEditorValidationAndGroups(t *testing.T) {
	e := newEnv(t, false)
	f := lemonForm()
	f.Set("title", "")
	f.Set("servings", "lots")
	res, body := e.post(t, "/new", f)
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
	wantContains(t, body, "Please fix the highlighted fields", "Servings must be a number",
		"2 cups all-purpose flour") // the user's text survives

	f = lemonForm()
	f.Set("title", "")
	f.Set("action", "add-ingredient-group")
	res, body = e.post(t, "/new", f)
	wantStatus(t, res, 200, body)
	if n := strings.Count(body, `name="ig_lines"`); n != 3 {
		t.Errorf("after + group: %d ingredient textareas, want 3", n)
	}
	if strings.Contains(body, "Please fix") {
		t.Error("+ group must not validate")
	}
}

func TestDeleteUndoTrashAndPurge(t *testing.T) {
	e := newEnv(t, true)
	e.post(t, "/new", lemonForm())

	res, body := e.post(t, "/r/example-lemon-cream-bars/delete", nil)
	wantStatus(t, res, http.StatusSeeOther, body)
	_, body = e.get(t, res.Header.Get("Location"))
	wantContains(t, body, "Moved to the trash.", `action="/r/example-lemon-cream-bars/undelete"`, "Undo")

	res, body = e.get(t, "/r/example-lemon-cream-bars")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "This recipe is in the trash")
	if strings.Contains(body, "Start cooking") {
		t.Error("trashed recipe offers cooking")
	}

	_, body = e.get(t, "/trash")
	wantContains(t, body, "Example Lemon Cream Bars", "Empty the trash")

	res, body = e.post(t, "/r/example-lemon-cream-bars/undelete", nil)
	wantStatus(t, res, http.StatusSeeOther, body)
	_, body = e.get(t, "/trash")
	wantContains(t, body, "The trash is empty.")

	// Emptying needs the confirmation box.
	e.post(t, "/r/example-lemon-cream-bars/delete", nil)
	e.post(t, "/trash/empty", url.Values{})
	if res, _ := e.get(t, "/r/example-lemon-cream-bars"); res.StatusCode != 200 {
		t.Fatal("emptied without confirmation")
	}
	res, body = e.post(t, "/trash/empty", url.Values{"confirm": {"yes"}})
	wantStatus(t, res, http.StatusSeeOther, body)
	if res, _ := e.get(t, "/r/example-lemon-cream-bars"); res.StatusCode != 404 {
		t.Errorf("after empty: %d, want 404", res.StatusCode)
	}
}

func TestEmptyTrashIsAdminOnly(t *testing.T) {
	e := newEnv(t, false)
	_, body := e.get(t, "/trash")
	if strings.Contains(body, "Empty the trash") {
		t.Error("non-admin sees the empty-trash form")
	}
	res, _ := e.post(t, "/trash/empty", url.Values{"confirm": {"yes"}})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin empty: %d, want 403", res.StatusCode)
	}
}

func TestHistoryAndRestore(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	f := lemonForm()
	f.Set("revision", "1")
	f.Set("title", "Renamed Bars")
	e.post(t, "/r/example-lemon-cream-bars", f)

	res, body := e.get(t, "/r/example-lemon-cream-bars/history")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Revision 2", "Revision 1", "by Test User", "current")

	res, body = e.get(t, "/r/example-lemon-cream-bars/history/1")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "revision 1 of 2", "Example Lemon Cream Bars", "Restore this version")

	res, body = e.post(t, "/r/example-lemon-cream-bars/history/1/restore", nil)
	wantStatus(t, res, http.StatusSeeOther, body)
	rec, _ := e.svc.Get(context.Background(), "example-lemon-cream-bars")
	if rec.Title != "Example Lemon Cream Bars" || rec.Revision != 3 {
		t.Errorf("after restore: %q rev %d", rec.Title, rec.Revision)
	}
	if res, _ := e.get(t, "/r/example-lemon-cream-bars/history/99"); res.StatusCode != 404 {
		t.Errorf("missing revision: %d", res.StatusCode)
	}
}

func TestCookAndBrowse(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	res, body := e.get(t, "/r/example-lemon-cream-bars/cook?step=99")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Step 2 of 2", `data-seconds="1200"`, ">Done<")

	res, body = e.get(t, "/search")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "All recipes", "1 recipe", "Example Lemon Cream Bars")

	for _, p := range []string{"/r/nope", "/r/nope/cook", "/r/nope/edit", "/r/nope/history"} {
		if res, _ := e.get(t, p); res.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, res.StatusCode)
		}
	}
}

// Every page must work under the CSP: no inline scripts, no inline styles,
// no inline event handlers.
func TestNoInlineCode(t *testing.T) {
	e := newEnv(t, true)
	e.post(t, "/new", lemonForm())
	e.post(t, "/new", func() url.Values { f := lemonForm(); f.Set("title", "Trashed One"); return f }())
	e.post(t, "/r/trashed-one/delete", nil)
	bad := regexp.MustCompile(`(?i)\sstyle=|\son[a-z]+=|<style`)
	script := regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	for _, p := range []string{"/", "/?deleted=x", "/browse", "/new", "/trash", "/r/example-lemon-cream-bars",
		"/r/example-lemon-cream-bars/edit", "/r/example-lemon-cream-bars/cook", "/r/example-lemon-cream-bars/history",
		"/r/example-lemon-cream-bars/history/1", "/r/trashed-one", "/r/missing"} {
		res, body := e.get(t, p)
		if m := bad.FindString(body); m != "" {
			t.Errorf("%s: inline code %q", p, m)
		}
		for _, m := range script.FindAllStringSubmatch(body, -1) {
			if !strings.Contains(m[1], "src=") || strings.TrimSpace(m[2]) != "" {
				t.Errorf("%s: inline script %q", p, m[0])
			}
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s: CSP = %q", p, csp)
		}
	}
}

func TestStatic(t *testing.T) {
	e := newEnv(t, false)
	u := asset("css/app.css")
	res, body := e.get(t, u)
	if res.StatusCode != 200 || !strings.Contains(body, "--accent") {
		t.Fatalf("GET %s = %d", u, res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned asset Cache-Control = %q", cc)
	}
	if res, _ := e.get(t, "/static/css/"); res.StatusCode != 404 {
		t.Errorf("directory listing status = %d, want 404", res.StatusCode)
	}
}

func TestManifestAndIcons(t *testing.T) {
	e := newEnv(t, false)
	res, body := e.get(t, asset("manifest.webmanifest"))
	if res.StatusCode != 200 {
		t.Fatalf("manifest status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type = %q", ct)
	}
	var m struct {
		Name  string `json:"name"`
		Icons []struct {
			Src     string `json:"src"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if m.Name != "Kitchen Grimoire" || len(m.Icons) == 0 {
		t.Errorf("manifest = %+v", m)
	}
	for _, ic := range m.Icons {
		if res, _ := e.get(t, ic.Src); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
			t.Errorf("icon %s: status %d, type %q", ic.Src, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	_, page := e.get(t, "/")
	for _, want := range []string{`rel="manifest"`, `rel="icon"`, `rel="apple-touch-icon"`, `name="theme-color"`, "img/logo-mark.svg"} {
		if !strings.Contains(page, want) {
			t.Errorf("home page missing %s", want)
		}
	}
}
