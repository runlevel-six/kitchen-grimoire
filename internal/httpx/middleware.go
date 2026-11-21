// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// statusWriter records the status code and size written by a handler.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
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
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (for
// flushing and deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Logging logs one line per request: id, method, route pattern, status,
// size, duration, and the user. It never logs bodies (§14.5).
// It wraps the auth middleware, which reports the user back through a
// holder placed in the request context. routeOf maps a request to its route
// pattern; middleware in between copies the request, so the pattern that
// ServeMux records on its own copy isn't visible here. MuxRoute(mux) is the
// usual choice.
func Logging(log *slog.Logger, routeOf func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID()
		w.Header().Set("X-Request-Id", id)
		sw := &statusWriter{ResponseWriter: w}
		holder := &userHolder{}
		next.ServeHTTP(sw, r.WithContext(withHolder(r.Context(), holder)))
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		route := r.Pattern
		if routeOf != nil {
			route = routeOf(r)
		}
		if route == "" {
			route = "unmatched"
		}
		level := slog.LevelInfo
		switch {
		case sw.status >= 500:
			level = slog.LevelError
		case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
			level = slog.LevelDebug // probes would drown everything else
		}
		log.LogAttrs(r.Context(), level, "request",
			slog.String("id", id),
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", sw.status),
			slog.Int("bytes", sw.bytes),
			slog.Duration("duration", time.Since(start)),
			slog.String("user", holder.name),
		)
	})
}

// Recover turns a handler panic into a 500 and a logged stack trace, so
// one bad request can't take the server down.
func Recover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // the server's own signal to drop the connection
				}
				log.ErrorContext(r.Context(), "panic in handler", "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// userHolder lets the logging middleware learn who the request belonged to
// after the auth middleware (which runs inside it) has identified them.
type userHolder struct{ name string }

type holderKey struct{}

func withHolder(ctx context.Context, h *userHolder) context.Context {
	return context.WithValue(ctx, holderKey{}, h)
}

// noteUser records the user for the request log, if Logging is in use.
func noteUser(ctx context.Context, name string) {
	if h, ok := ctx.Value(holderKey{}).(*userHolder); ok {
		h.name = name
	}
}

// MuxRoute returns a routeOf function for Logging that asks mux which
// pattern a request matches, without serving it.
func MuxRoute(mux *http.ServeMux) func(*http.Request) string {
	return func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	}
}
