// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSearchAndBrowsePages(t *testing.T) {
	e := newEnv(t, false)
	e.post(t, "/new", lemonForm())
	f := lemonForm()
	f.Set("title", "Weeknight Chili")
	f.Set("ig_lines", "1 lb ground beef")
	f.Set("categories", "Main Dishes > Beef")
	f.Set("contributed_by", "Uncle Example")
	f.Set("tags", "weeknight")
	e.post(t, "/new", f)

	res, body := e.get(t, "/search?q=lemon")
	wantStatus(t, res, http.StatusOK, body)
	wantContains(t, body, "Results for “lemon”", "1 recipe", "Example Lemon Cream Bars")
	if strings.Contains(body, "Weeknight Chili") {
		t.Error("lemon search returned the chili")
	}

	_, body = e.get(t, "/search?q=beef")
	wantContains(t, body, "Weeknight Chili")

	res, body = e.get(t, "/browse")
	wantStatus(t, res, http.StatusOK, body)
	wantContains(t, body, "All 2 recipes", "Desserts", "Cookies", "Main Dishes", "Beef",
		"Aunt Example", "Uncle Example", "Example Family Cookbook", "#weeknight")

	_, body = e.get(t, "/browse/category/desserts")
	wantContains(t, body, "Category: desserts", "Example Lemon Cream Bars", "Remove filter")
	if strings.Contains(body, "Weeknight Chili") {
		t.Error("desserts listed the chili")
	}
	_, body = e.get(t, "/browse/contributor/"+url.PathEscape("Uncle Example"))
	wantContains(t, body, "From Uncle Example", "Weeknight Chili")

	_, body = e.get(t, "/search?tag=weeknight&q=chili")
	wantContains(t, body, "Tag: weeknight", "Weeknight Chili")
	// The chip removes only its own filter.
	wantContains(t, body, `href="/search?q=chili"`)

	_, body = e.get(t, "/search?q="+url.QueryEscape(`NEAR( "" * ^ -`))
	wantContains(t, body, "Nothing matched") // nonsense never errors

	_, body = e.get(t, "/review")
	wantContains(t, body, "Review queue", "0 recipes")
}
