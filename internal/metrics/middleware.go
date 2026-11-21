// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// Names of the HTTP metrics recorded by Middleware (§14.5).
const (
	HTTPRequestsName = "grimoire_http_requests_total"
	HTTPDurationName = "grimoire_http_duration_seconds"
)

// httpMetrics registers the HTTP request metrics on first use, so that a
// registry used without Middleware doesn't export empty families.
func (r *Registry) httpMetrics() (*CounterVec, *HistogramVec) {
	r.httpOnce.Do(func() {
		r.httpRequests = r.Counter(HTTPRequestsName,
			"HTTP requests served, by route pattern and status code.", "route", "code")
		r.httpDuration = r.Histogram(HTTPDurationName,
			"HTTP request latency in seconds, by route pattern.", DefaultBuckets, "route")
	})
	return r.httpRequests, r.httpDuration
}

// Middleware counts requests and records their latency in reg.
//
// The route label comes from routeOf (usually httpx.MuxRoute(mux)), which
// returns the matched pattern such as "GET /r/{slug}". It is never the raw
// URL path: every distinct label value is a separate time series, and raw
// paths (one per recipe, plus whatever scanners probe) would grow without
// bound. Requests that match no route are labelled "unmatched".
func Middleware(reg *Registry, routeOf func(*http.Request) string, next http.Handler) http.Handler {
	requests, duration := reg.httpMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		// Record even if a handler panics (the panic continues upward).
		defer func() {
			route := ""
			if routeOf != nil {
				route = routeOf(r)
			}
			if route == "" {
				route = "unmatched"
			}
			code := sw.status
			if code == 0 {
				code = http.StatusOK
			}
			requests.Inc(route, strconv.Itoa(code))
			duration.Observe(time.Since(start).Seconds(), route)
		}()
		next.ServeHTTP(sw, r)
	})
}

// statusWriter remembers the status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer (for
// flushing and deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
