// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

const capturedPage = `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@type":"Recipe","name":"Example Captured Soup",
"recipeIngredient":["1 onion","4 cups stock"],"recipeInstructions":"Simmer 20 minutes."}
</script></head><body></body></html>`

func (e *env) api(t *testing.T, token string, body any) (*http.Response, map[string]string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capture", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Result(), out
}

func TestSettingsTokensAndPhoneCapture(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()

	res, body := e.get(t, "/settings")
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Save to Kitchen Grimoire", `href="javascript:`, "https://recipes.example.com/api/v1/capture")

	res, body = e.post(t, "/settings/tokens", url.Values{"name": {"My iPhone"}})
	wantStatus(t, res, 200, body)
	secret := regexp.MustCompile(`kg_[A-Za-z0-9_-]{40,}`).FindString(body)
	if secret == "" {
		t.Fatal("new token not shown")
	}
	_, body = e.get(t, "/settings")
	if strings.Contains(body, secret) {
		t.Fatal("token shown a second time")
	}
	wantContains(t, body, "My iPhone", "never used")

	// Capture by token, then open the draft as the same (signed-in) user.
	res2, out := e.api(t, secret, map[string]string{"url": "https://recipes.example.org/soup", "html": capturedPage})
	if res2.StatusCode != http.StatusCreated || !strings.HasPrefix(out["url"], "https://recipes.example.com/capture/d/") {
		t.Fatalf("capture API: %d %v", res2.StatusCode, out)
	}
	draft := strings.TrimPrefix(out["url"], "https://recipes.example.com")
	res, body = e.get(t, draft)
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Check and save", "Example Captured Soup", `name="source_kind" value="capture"`)

	form := formValues(body)
	form.Set("action", "save")
	res, body = e.post(t, "/import/save", form)
	wantStatus(t, res, http.StatusSeeOther, body)
	rec, err := e.svc.Get(ctx, "example-captured-soup")
	if err != nil || rec.SourceKind != recipe.SourceCapture || rec.SourceURL != "https://recipes.example.org/soup" {
		t.Fatalf("saved capture = %+v, %v", rec, err)
	}

	// Another user's capture isn't visible to this user.
	other, _ := e.st.UpsertUser(ctx, "someone-else", "", "")
	_, otherSecret, _ := e.st.CreateToken(ctx, other.ID, "theirs")
	_, out = e.api(t, otherSecret, map[string]string{"url": "https://recipes.example.org/soup", "html": capturedPage})
	res, _ = e.get(t, strings.TrimPrefix(out["url"], "https://recipes.example.com"))
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("someone else's draft: %d, want 404", res.StatusCode)
	}

	// Bad, missing, and revoked tokens.
	if res, _ := e.api(t, "kg_nope", map[string]string{"url": "https://x.example", "html": capturedPage}); res.StatusCode != 401 {
		t.Errorf("bad token: %d", res.StatusCode)
	}
	if res, _ := e.api(t, "", map[string]string{}); res.StatusCode != 401 {
		t.Errorf("no token: %d", res.StatusCode)
	}
	toks, _ := e.st.Tokens(ctx, "")
	for _, tk := range toks {
		if tk.Name == "My iPhone" {
			e.post(t, "/settings/tokens/"+tk.ID+"/revoke", nil)
		}
	}
	if res, _ := e.api(t, secret, map[string]string{"url": "https://x.example", "html": capturedPage}); res.StatusCode != 401 {
		t.Errorf("revoked token: %d", res.StatusCode)
	}
	// Not a recipe.
	if res, _ := e.api(t, otherSecret, map[string]string{"url": "https://x.example", "html": "<html></html>"}); res.StatusCode != 422 {
		t.Errorf("no recipe: %d", res.StatusCode)
	}
}

func TestBookmarkletCaptureSubmit(t *testing.T) {
	e := newEnv(t, false)
	res, body := e.get(t, "/capture")
	wantStatus(t, res, 200, body)
	wantContains(t, body, `id="capture-form"`, "js/capture.js")
	res, body = e.post(t, "/capture", url.Values{"url": {"https://recipes.example.org/soup"}, "html": {capturedPage}})
	wantStatus(t, res, 200, body)
	wantContains(t, body, "Check and save", "Example Captured Soup")
	res, body = e.post(t, "/capture", url.Values{"url": {"javascript:alert(1)"}, "html": {capturedPage}})
	wantStatus(t, res, http.StatusUnprocessableEntity, body)
}

func TestCaptureRateLimit(t *testing.T) {
	e := newEnv(t, false)
	u, _ := e.st.UpsertUser(context.Background(), "busy", "", "")
	_, secret, _ := e.st.CreateToken(context.Background(), u.ID, "t")
	var last int
	for range 31 {
		res, _ := e.api(t, secret, map[string]string{"url": "https://recipes.example.org/soup", "html": capturedPage})
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("31st capture in an hour: %d, want 429", last)
	}
}

func TestAdminImportPage(t *testing.T) {
	e := newEnv(t, true)
	line, _ := exportfmt.MarshalLine(&recipe.Recipe{Title: "Example Bundled Bread", ImportKey: "test:1",
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "3 cups flour"}}}}})
	var zipBuf bytes.Buffer
	if err := exportfmt.WriteBundleLines(&zipBuf, [][]byte{line}); err != nil {
		t.Fatal(err)
	}
	upload := func(dry string) string {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("dry_run", dry)
		fw, _ := mw.CreateFormFile("bundle", "b.zip")
		_, _ = io.Copy(fw, bytes.NewReader(zipBuf.Bytes()))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/settings/import", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	wantContains(t, upload("1"), "Dry run:", "1 created")
	if _, err := e.svc.Get(context.Background(), "example-bundled-bread"); err == nil {
		t.Fatal("dry run wrote a recipe")
	}
	wantContains(t, upload("0"), "1 created")
	if _, err := e.svc.Get(context.Background(), "example-bundled-bread"); err != nil {
		t.Fatal("import did not create the recipe")
	}
	// Non-admins can't.
	f := newEnv(t, false)
	res, _ := f.post(t, "/settings/import", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin import: %d", res.StatusCode)
	}
}
