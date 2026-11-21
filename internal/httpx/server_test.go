// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthz(t *testing.T) {
	mux := NewMux()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s /healthz = %d, want 200", method, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Grab a free port, then release it for Serve to bind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, NewServer(addr, NewMux(), log), log) }()

	// Wait for the server to come up.
	url := "http://" + addr + "/healthz"
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestServeListenError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// The port is taken, so Serve must fail fast without waiting on ctx.
	err = Serve(context.Background(), NewServer(ln.Addr().String(), NewMux(), log), log)
	if err == nil {
		t.Fatal("Serve on a busy port returned nil")
	}
}
