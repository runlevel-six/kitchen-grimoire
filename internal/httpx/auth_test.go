// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

type fakeUsers struct {
	mu    sync.Mutex
	calls int
	byRU  map[string]recipe.User
}

func (f *fakeUsers) UpsertUser(_ context.Context, ru, name, email string) (recipe.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.byRU == nil {
		f.byRU = map[string]recipe.User{}
	}
	u, ok := f.byRU[ru]
	if !ok {
		u = recipe.User{ID: "id-" + ru, RemoteUser: ru}
	}
	if name != "" {
		u.DisplayName = name
	}
	if email != "" {
		u.Email = email
	}
	f.byRU[ru] = u
	return u, nil
}

func authHandler(cfg AuthConfig, users UserStore) http.Handler {
	if cfg.UserHeader == "" {
		cfg.UserHeader, cfg.NameHeader, cfg.EmailHeader = "Remote-User", "Remote-Name", "Remote-Email"
	}
	a := NewAuth(cfg, users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			_, _ = w.Write([]byte("anonymous"))
			return
		}
		admin := ""
		if u.IsAdmin {
			admin = " admin"
		}
		_, _ = w.Write([]byte(u.RemoteUser + "|" + u.Name() + admin))
	}))
}

func do(h http.Handler, peer, path string, headers map[string]string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestTrustedHeaderBoundary(t *testing.T) {
	cfg := AuthConfig{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("fd00::/8")},
		Admins:         []string{"Alice"},
		Public:         []string{"/healthz", "/static/"},
	}
	h := authHandler(cfg, &fakeUsers{})
	alice := map[string]string{"Remote-User": "alice", "Remote-Name": "Alice Example"}

	tests := []struct {
		name    string
		peer    string
		path    string
		headers map[string]string
		code    int
		body    string
	}{
		{"trusted proxy with identity", "10.42.1.7:5555", "/", alice, 200, "alice|Alice Example admin"},
		{"trusted IPv6 proxy", "[fd00::5]:443", "/", alice, 200, "alice|Alice Example admin"},
		{"IPv4-mapped IPv6 peer", "[::ffff:10.42.1.7]:5555", "/", alice, 200, "alice|Alice Example admin"},
		{"untrusted peer claiming identity", "203.0.113.9:4444", "/", alice, 401, "unauthorized"},
		{"pod-network peer outside the proxy range", "10.43.0.1:1", "/", alice, 401, "unauthorized"},
		{"trusted proxy without identity", "10.42.1.7:5555", "/", nil, 401, "unauthorized"},
		{"control characters in user header", "10.42.1.7:5555", "/", map[string]string{"Remote-User": "al\x01ice"}, 401, "unauthorized"},
		{"overlong user header", "10.42.1.7:5555", "/", map[string]string{"Remote-User": strings.Repeat("a", 300)}, 401, "unauthorized"},
		{"non-admin user", "10.42.1.7:5555", "/", map[string]string{"Remote-User": "bob"}, 200, "bob|bob"},
		{"public health check", "203.0.113.9:4444", "/healthz", nil, 200, "anonymous"},
		{"public static prefix", "203.0.113.9:4444", "/static/css/app.css", nil, 200, "anonymous"},
		{"prefix match is only for paths ending in /", "203.0.113.9:4444", "/healthzzz", nil, 401, "unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := do(h, tt.peer, tt.path, tt.headers)
			if code != tt.code || body != tt.body {
				t.Errorf("got %d %q, want %d %q", code, body, tt.code, tt.body)
			}
		})
	}
}

func TestAdminGroups(t *testing.T) {
	cfg := AuthConfig{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
		GroupsHeader:   "Remote-Groups",
		AdminGroups:    []string{"admins"},
	}
	h := authHandler(cfg, &fakeUsers{})
	tests := []struct {
		peer, groups, want string
		code               int
	}{
		{"10.42.0.1:1", "family, Admins", "dana|dana admin", 200},
		{"10.42.0.1:1", "family", "dana|dana", 200},
		{"10.42.0.1:1", "", "dana|dana", 200},
		{"203.0.113.1:1", "admins", "unauthorized", 401}, // never believed from outside
	}
	for _, tt := range tests {
		code, body := do(h, tt.peer, "/", map[string]string{"Remote-User": "dana", "Remote-Groups": tt.groups})
		if code != tt.code || body != tt.want {
			t.Errorf("groups %q from %s: %d %q, want %d %q", tt.groups, tt.peer, code, body, tt.code, tt.want)
		}
	}
}

func TestDevMode(t *testing.T) {
	h := authHandler(AuthConfig{None: true}, &fakeUsers{})
	code, body := do(h, "203.0.113.9:1", "/", map[string]string{"Remote-User": "mallory"})
	if code != 200 || body != "dev|Developer admin" {
		t.Errorf("dev mode = %d %q, want the fixed dev user regardless of headers", code, body)
	}
}

func TestUserLookupIsCached(t *testing.T) {
	users := &fakeUsers{}
	h := authHandler(AuthConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}, users)
	for range 5 {
		do(h, "127.0.0.1:1", "/", map[string]string{"Remote-User": "carol"})
	}
	if users.calls != 1 {
		t.Errorf("UpsertUser called %d times for 5 requests, want 1 (cached)", users.calls)
	}
	// A changed display name is picked up immediately.
	_, body := do(h, "127.0.0.1:1", "/", map[string]string{"Remote-User": "carol", "Remote-Name": "Carol E."})
	if body != "carol|Carol E." {
		t.Errorf("renamed user = %q", body)
	}
}

func TestLoggingRecordsUser(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	users := &fakeUsers{}
	a := NewAuth(AuthConfig{None: true}, users, log)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := Logging(log, MuxRoute(mux), a.Middleware(mux))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/42", nil))
	line := buf.String()
	for _, want := range []string{"route=\"GET /x/{id}\"", "status=418", "user=dev"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q missing %q", line, want)
		}
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id header")
	}
}

func TestRecover(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := Recover(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panic → %d, want 500", rec.Code)
	}
}
