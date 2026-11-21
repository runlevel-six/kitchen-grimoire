// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFavorites(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	f := lemonForm()
	f.Set("title", "Weeknight Chili")
	e.post(t, "/new", f)

	_, body := e.get(t, "/r/example-lemon-cream-bars")
	wantContains(t, body, "☆ Favorite")

	// htmx: only the button comes back.
	req := httptest.NewRequest(http.MethodPost, "/r/example-lemon-cream-bars/favorite", strings.NewReader("on=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "★ Favorite") || strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("htmx favorite: %d %q", rec.Code, rec.Body.String())
	}

	_, body = e.get(t, "/")
	wantContains(t, body, "Your favorites")
	_, body = e.get(t, "/search?fav=1")
	wantContains(t, body, "Favorites", "1 recipe", "Example Lemon Cream Bars")
	if strings.Contains(body, "Weeknight Chili") {
		t.Error("favorites filter listed a non-favorite")
	}

	// Without JS: plain form post and redirect.
	res, _ := e.post(t, "/r/example-lemon-cream-bars/favorite", url.Values{"on": {"0"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("no-JS unfavorite: %d", res.StatusCode)
	}
	_, body = e.get(t, "/search?fav=1")
	wantContains(t, body, "0 recipes")
}
