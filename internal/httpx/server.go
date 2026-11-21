// SPDX-License-Identifier: Apache-2.0

// Package httpx holds the HTTP server plumbing: routing of operational
// endpoints, middleware, and graceful shutdown.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ShutdownTimeout bounds how long in-flight requests may run after a
// shutdown signal before the server closes them.
const ShutdownTimeout = 20 * time.Second

// ReadyFunc reports whether the server can do useful work (e.g. the
// database answers). A nil ReadyFunc means always ready.
type ReadyFunc func(context.Context) error

// readyTimeout bounds a readiness check so a stuck disk fails the probe
// instead of hanging it.
const readyTimeout = 2 * time.Second

// NewMux returns the app-port router with the operational endpoints. Feature
// handlers are registered on it by the caller.
func NewMux(ready ReadyFunc) *http.ServeMux {
	mux := http.NewServeMux()
	// Go 1.22+ patterns: "METHOD /path". GET also matches HEAD.
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(ready))
	return mux
}

// readyz reports readiness: Kubernetes sends traffic only while it is 200.
// Unlike healthz, it does check dependencies.
func readyz(ready ReadyFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if ready != nil {
			ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
			defer cancel()
			if err := ready(ctx); err != nil {
				// The reason goes to the log, not the response.
				slog.WarnContext(r.Context(), "not ready", "err", err)
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}

// healthz reports liveness: the process is up and serving HTTP. It must not
// depend on the database, or a slow disk would get the pod restarted.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

// NewServer wraps h in an http.Server with conservative timeouts.
func NewServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // uploads and bundle imports need headroom
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Serve listens on srv.Addr and serves until ctx is cancelled, then shuts
// down gracefully. It returns nil after a clean shutdown.
//
// The listener is opened before Serve returns control to the goroutine, so a
// bad address (port in use, permission denied) is reported immediately as an
// error rather than after the first signal.
func Serve(ctx context.Context, srv *http.Server, log *slog.Logger) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", srv.Addr, err)
	}
	log.Info("listening", "addr", ln.Addr().String())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		// Serve only returns early on a real failure.
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", ShutdownTimeout.String())
	// The parent ctx is already cancelled, so derive the deadline from a fresh
	// context. WithoutCancel keeps its values (none today) but drops the
	// cancellation.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// After Shutdown, Serve returns http.ErrServerClosed; that is the normal case.
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}
