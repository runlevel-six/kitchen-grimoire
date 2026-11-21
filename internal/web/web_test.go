// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
)

func newServer(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	New(slog.New(slog.NewTextHandler(io.Discard, nil)), SampleRecipes()).Register(mux)
	return httpx.SecurityHeaders(mux)
}

func get(t *testing.T, h http.Handler, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestPages(t *testing.T) {
	h := newServer(t)
	tests := []struct {
		path string
		code int
		want []string
	}{
		{"/", 200, []string{"Example Lemon Cream Bars", "Example Weeknight Chili"}},
		{"/r/example-lemon-cream-bars", 200, []string{
			"From Aunt Example · Example Family Cookbook",
			"1 8-oz pkg cream cheese, softened",
			"View original",
			`aria-pressed="true"`,
		}},
		{"/r/example-lemon-cream-bars?x=2", 200, []string{
			"2 8-oz pkg cream cheese, softened", // size is never scaled
			"6–8 Tbsp lemon juice",              // range scales both ends
			"not scaled",                        // unparsed line is marked
			"Makes 48",
		}},
		{"/r/example-lemon-cream-bars?x=7", 200, []string{"1 8-oz pkg cream cheese"}}, // unknown factor → 1×
		{"/r/example-weeknight-chili", 200, []string{"By Example Test Kitchen · recipes.example.com"}},
		{"/r/example-lemon-cream-bars/cook", 200, []string{`Step 1 of 7`, `data-seconds="1200"`, "Leave cook mode"}},
		{"/r/example-lemon-cream-bars/cook?step=99", 200, []string{`Step 7 of 7`, ">Done<"}},
		{"/r/nope", 404, nil},
		{"/r/nope/cook", 404, nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			res, body := get(t, h, tt.path)
			if res.StatusCode != tt.code {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.code)
			}
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body missing %q", w)
				}
			}
		})
	}
}

// Every page must work under the CSP: no inline scripts, no inline styles,
// no inline event handlers.
func TestNoInlineCode(t *testing.T) {
	h := newServer(t)
	bad := regexp.MustCompile(`(?i)\sstyle=|\son[a-z]+=|<style`)
	// Every <script> tag must load a file and have no body.
	script := regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	for _, p := range []string{"/", "/r/example-lemon-cream-bars", "/r/example-lemon-cream-bars/cook"} {
		res, body := get(t, h, p)
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
	h := newServer(t)
	url := asset("css/app.css")
	if !strings.Contains(url, "?v=") {
		t.Fatalf("asset URL has no version: %q", url)
	}
	res, body := get(t, h, url)
	if res.StatusCode != 200 || !strings.Contains(body, "--accent") {
		t.Fatalf("GET %s = %d", url, res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned asset Cache-Control = %q", cc)
	}
	if res, _ := get(t, h, "/static/css/"); res.StatusCode != 404 {
		t.Errorf("directory listing status = %d, want 404", res.StatusCode)
	}
	if res, _ := get(t, h, asset("vendor/htmx-2.0.11.min.js")); res.StatusCode != 200 {
		t.Errorf("htmx status = %d", res.StatusCode)
	}
}
