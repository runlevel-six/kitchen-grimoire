// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func scrape(t *testing.T, reg *Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, ContentType)
	}
	return rec.Body.String()
}

func TestExposition(t *testing.T) {
	reg := NewRegistry()
	c := reg.Counter("test_events_total", "Events.\nSecond line with a \\ backslash.", "kind")
	c.Inc("a")
	c.Add(2.5, "a")
	c.Inc(`we"ird\` + "\n")
	h := reg.Histogram("test_latency_seconds", "Latency.", []float64{0.1, 1}, "route")
	h.Observe(0.05, "/x")
	h.Observe(0.1, "/x") // on the boundary: counts in le="0.1"
	h.Observe(3, "/x")
	g := reg.Gauge("test_last_success_timestamp", "Last success.")
	reg.GaugeFunc("test_build_info", "Build.", func(context.Context) (float64, error) { return 1, nil },
		Label{"version", "v1.2.3"}, Label{"go", "go1.x"})
	reg.GaugeFunc("test_broken", "Fails.", func(context.Context) (float64, error) { return 0, errors.New("db down") })

	out := scrape(t, reg)
	want := []string{
		"# HELP test_events_total Events.\\nSecond line with a \\\\ backslash.\n# TYPE test_events_total counter\n",
		"test_events_total{kind=\"a\"} 3.5\n",
		`test_events_total{kind="we\"ird\\\n"} 1` + "\n",
		"# TYPE test_latency_seconds histogram\n",
		"test_latency_seconds_bucket{route=\"/x\",le=\"0.1\"} 2\n",
		"test_latency_seconds_bucket{route=\"/x\",le=\"1\"} 2\n",
		"test_latency_seconds_bucket{route=\"/x\",le=\"+Inf\"} 3\n",
		"test_latency_seconds_sum{route=\"/x\"} 3.15\n",
		"test_latency_seconds_count{route=\"/x\"} 3\n",
		"# TYPE test_last_success_timestamp gauge\n",
		"test_build_info{version=\"v1.2.3\",go=\"go1.x\"} 1\n",
		"# TYPE test_broken gauge\n",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("exposition missing %q\n--- got ---\n%s", w, out)
		}
	}
	// Unset gauge and failing callback export no sample line.
	for _, bad := range []string{"test_last_success_timestamp ", "test_broken "} {
		if strings.Contains(out, "\n"+bad) {
			t.Errorf("unexpected sample %q in\n%s", bad, out)
		}
	}
	// Families come out sorted by name.
	if strings.Index(out, "test_broken") > strings.Index(out, "test_events_total") {
		t.Error("families not sorted by name")
	}

	g.Set(1700000000)
	if out := scrape(t, reg); !strings.Contains(out, "\ntest_last_success_timestamp 1.7e+09\n") {
		t.Errorf("gauge after Set missing:\n%s", out)
	}
}

func TestDuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Gauge("x", "x")
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration did not panic")
		}
	}()
	reg.Counter("x", "x")
}

func TestMiddlewareUsesRoutePattern(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /r/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("slug") == "missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	routeOf := func(r *http.Request) string { _, p := mux.Handler(r); return p }
	h := Middleware(reg, routeOf, mux)

	for _, path := range []string{"/r/one", "/r/two", "/r/missing", "/nope/123", "/nope/456"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	out := scrape(t, reg)
	for _, w := range []string{
		`grimoire_http_requests_total{route="GET /r/{slug}",code="200"} 2`,
		`grimoire_http_requests_total{route="GET /r/{slug}",code="404"} 1`,
		`grimoire_http_requests_total{route="unmatched",code="404"} 2`,
		`grimoire_http_duration_seconds_count{route="GET /r/{slug}"} 3`,
		`grimoire_http_duration_seconds_bucket{route="unmatched",le="+Inf"} 2`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	for _, raw := range []string{"/r/one", "/nope/123"} {
		if strings.Contains(out, raw) {
			t.Errorf("raw path %q leaked into labels:\n%s", raw, out)
		}
	}
}
