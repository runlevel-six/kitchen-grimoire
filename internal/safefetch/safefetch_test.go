// SPDX-License-Identifier: Apache-2.0

package safefetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

// stubResolver answers from a fixed table instead of real DNS. If a name
// has several entries in seq, successive lookups walk through them (the
// last one repeats), which lets a test change the answer between lookups
// the way a DNS-rebinding attacker would.
type stubResolver struct {
	mu    sync.Mutex
	seq   map[string][]string
	calls map[string]int
}

func newStub(m map[string][]string) *stubResolver {
	return &stubResolver{seq: m, calls: map[string]int{}}
}

func (s *stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	answers, ok := s.seq[strings.ToLower(host)]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := min(s.calls[host], len(answers)-1)
	s.calls[host]++
	return []netip.Addr{netip.MustParseAddr(answers[i])}, nil
}

// testEnv is an httptest server reachable as http://allowed.test:<port>,
// where allowed.test is allowlisted and resolves to the server's 127.0.0.1.
// evil.test also resolves to 127.0.0.1 but is NOT allowlisted.
type testEnv struct {
	srv  *httptest.Server
	mux  *http.ServeMux
	port string
	res  *stubResolver
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &testEnv{
		srv:  srv,
		mux:  mux,
		port: u.Port(),
		res: newStub(map[string][]string{
			"allowed.test": {"127.0.0.1"},
			"evil.test":    {"127.0.0.1"},
		}),
	}
}

func (e *testEnv) client(mod func(*Config)) *Client {
	cfg := Config{AllowHosts: []string{"Allowed.Test"}, Resolver: e.res}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg)
}

func (e *testEnv) url(path string) string {
	return "http://allowed.test:" + e.port + path
}

func html(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<html>ok</html>")
}

// noDial is a DialContext that fails the test if it is ever reached: used
// where the address check must stop the request before any connection.
func noDial(t *testing.T) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, _, addr string) (net.Conn, error) {
		t.Errorf("dial reached for %s; the address check should have blocked it", addr)
		return nil, errors.New("unexpected dial")
	}
}

func TestBlockedAtDial(t *testing.T) {
	for _, tc := range deniedSamples {
		ip := netip.MustParseAddr(tc.addr)
		if ip.Zone() != "" {
			continue // a resolver never returns a zone; covered by TestDeniedAddr
		}
		t.Run(tc.name, func(t *testing.T) {
			// A hostname that resolves to the denied address.
			c := New(Config{
				Resolver:    newStub(map[string][]string{"victim.test": {tc.addr}}),
				DialContext: noDial(t),
			})
			_, err := c.FetchPage(context.Background(), "http://victim.test/")
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("via hostname: err = %v, want ErrBlocked", err)
			}
			// The same address as a literal in the URL.
			host := ip.String()
			if ip.Is6() {
				host = "[" + host + "]"
			}
			_, err = c.FetchPage(context.Background(), "https://"+host+"/")
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("via literal: err = %v, want ErrBlocked", err)
			}
		})
	}
}

// TestControlHook checks the net.Dialer Control function on its own: it is
// what stops the real socket, independent of the pre-dial check.
func TestControlHook(t *testing.T) {
	c := New(Config{ExtraDeny: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}})
	ctl := c.controlFunc(false)
	for _, a := range []string{"127.0.0.1:80", "[::ffff:10.0.0.1]:443", "[fe80::1%en0]:80", "203.0.113.5:443"} {
		if err := ctl("tcp", a, nil); !errors.Is(err, ErrBlocked) {
			t.Errorf("control(%s) = %v, want ErrBlocked", a, err)
		}
	}
	for _, a := range []string{"93.184.216.34:80", "[2606:4700::1111]:443"} {
		if err := ctl("tcp", a, nil); err != nil {
			t.Errorf("control(%s) = %v, want nil", a, err)
		}
	}
	if err := c.controlFunc(true)("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("allowlisted control = %v, want nil", err)
	}

	// And wired into a real net.Dialer against a live loopback listener:
	// the socket is refused before it connects.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d := net.Dialer{Control: ctl}
	if conn, err := d.Dial("tcp", ln.Addr().String()); !errors.Is(err, ErrBlocked) {
		if conn != nil {
			conn.Close()
		}
		t.Errorf("real dial to %s: err = %v, want ErrBlocked", ln.Addr(), err)
	}
}

// TestRealDialerBlocked uses the real dialer (no DialContext override) to
// show a non-allowlisted name resolving to a live loopback server is refused.
func TestRealDialerBlocked(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	e.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { hits.Add(1); html(w, r) })
	// Allowlist the port check out of the way by using port 80 via a
	// literal-free name: evil.test -> 127.0.0.1. Port 80 is not our server,
	// but the request must never get as far as trying.
	c := e.client(nil)
	_, err := c.FetchPage(context.Background(), "http://evil.test/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("error should name the address for logs: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server was hit %d times", hits.Load())
	}
}

func TestAllowlistedFetch(t *testing.T) {
	e := newEnv(t)
	e.mux.HandleFunc("/page", html)
	c := e.client(nil)
	resp, err := c.FetchPage(context.Background(), e.url("/page"))
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "<html>ok</html>" || resp.StatusCode != 200 || resp.ContentType != "text/html" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.FinalURL.String() != e.url("/page") {
		t.Errorf("FinalURL = %s", resp.FinalURL)
	}
}

func TestSchemeAndPort(t *testing.T) {
	c := New(Config{DialContext: noDial(t), Resolver: newStub(nil)})
	for _, tc := range []struct {
		url  string
		want error
	}{
		{"ftp://example.com/x", ErrScheme},
		{"file:///etc/passwd", ErrScheme},
		{"gopher://example.com/", ErrScheme},
		{"javascript:alert(1)", ErrScheme},
		{"http://example.com:8080/", ErrPort},
		{"https://example.com:22/", ErrPort},
		{"http:///nohost", ErrInvalidURL},
		{"http://%zz/", ErrInvalidURL},
	} {
		if _, err := c.FetchPage(context.Background(), tc.url); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.url, err, tc.want)
		}
	}
	// Explicit :80 and :443 are fine as far as the port rule goes; the
	// stub resolver then fails the lookup, which is not a port error.
	for _, u := range []string{"http://example.com:80/", "https://example.com:443/", "HTTP://example.com/"} {
		if _, err := c.FetchPage(context.Background(), u); errors.Is(err, ErrPort) || errors.Is(err, ErrScheme) {
			t.Errorf("%s: err = %v, want a DNS error", u, err)
		}
	}
}

func TestRedirects(t *testing.T) {
	e := newEnv(t)
	e.mux.HandleFunc("/page", html)
	e.mux.HandleFunc("/to-evil", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.test/", http.StatusFound)
	})
	e.mux.HandleFunc("/to-loopback-literal", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/", http.StatusFound)
	})
	e.mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusMovedPermanently)
	})
	e.mux.HandleFunc("/to-evil-port", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.test:"+e.port+"/page", http.StatusFound)
	})
	e.mux.HandleFunc("/to-ftp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://allowed.test/file", http.StatusFound)
	})
	var loops atomic.Int32
	e.mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		loops.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	// /chain/N redirects N more times, then serves the page.
	e.mux.HandleFunc("/chain/{n}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscan(r.PathValue("n"), &n)
		if n == 0 {
			html(w, r)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/chain/%d", n-1), http.StatusFound)
	})

	c := e.client(nil)
	ctx := context.Background()
	for _, tc := range []struct {
		path string
		want error
	}{
		{"/to-evil", ErrBlocked},
		{"/to-loopback-literal", ErrBlocked},
		{"/to-metadata", ErrBlocked},
		{"/to-evil-port", ErrPort},
		{"/to-ftp", ErrScheme},
		{"/loop", ErrTooManyRedirects},
		{"/chain/6", ErrTooManyRedirects},
		{"/chain/5", nil},
	} {
		_, err := c.FetchPage(ctx, e.url(tc.path))
		if (tc.want == nil && err != nil) || !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.path, err, tc.want)
		}
	}
	// Initial request plus exactly MaxRedirects follow-ups.
	if got := loops.Load(); got != DefaultMaxRedirects+1 {
		t.Errorf("loop handler hit %d times, want %d", got, DefaultMaxRedirects+1)
	}

	// A custom limit, and a negative one meaning "follow none".
	c2 := e.client(func(cfg *Config) { cfg.MaxRedirects = 2 })
	if _, err := c2.FetchPage(ctx, e.url("/chain/2")); err != nil {
		t.Errorf("chain/2 with limit 2: %v", err)
	}
	if _, err := c2.FetchPage(ctx, e.url("/chain/3")); !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("chain/3 with limit 2: %v", err)
	}
	c3 := e.client(func(cfg *Config) { cfg.MaxRedirects = -1 })
	if _, err := c3.FetchPage(ctx, e.url("/chain/1")); !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("chain/1 with no redirects: %v", err)
	}
}

func TestContentTypeAndSize(t *testing.T) {
	e := newEnv(t)
	e.mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	e.mux.HandleFunc("/no-type", func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil // suppress Go's sniffing
		w.Write([]byte("<html></html>"))
	})
	e.mux.HandleFunc("/xhtml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "Application/XHTML+XML")
		fmt.Fprint(w, "<html/>")
	})
	e.mux.HandleFunc("/png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG"))
	})
	// big serves n bytes with the given type; ?cl=0 omits Content-Length
	// (chunked encoding), so only the read-time cap can catch it.
	big := func(ct string, n int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			if r.URL.Query().Get("cl") != "0" {
				w.Header().Set("Content-Length", fmt.Sprint(n))
			}
			chunk := bytes.Repeat([]byte("a"), 64<<10)
			for written := 0; written < n; {
				k := min(len(chunk), n-written)
				if _, err := w.Write(chunk[:k]); err != nil {
					return
				}
				written += k
				if f, ok := w.(http.Flusher); ok && r.URL.Query().Get("cl") == "0" {
					f.Flush()
				}
			}
		}
	}
	e.mux.HandleFunc("/page-max", big("text/html", MaxPageBytes))
	e.mux.HandleFunc("/page-over", big("text/html", MaxPageBytes+1))
	e.mux.HandleFunc("/img-max", big("image/jpeg", MaxImageBytes))
	e.mux.HandleFunc("/img-over", big("image/jpeg", MaxImageBytes+1))

	c := e.client(nil)
	ctx := context.Background()
	page := c.FetchPage
	img := c.FetchImage
	for _, tc := range []struct {
		name  string
		fetch func(context.Context, string) (*Response, error)
		path  string
		want  error
		n     int
	}{
		{"page json", page, "/json", ErrContentType, 0},
		{"page no type", page, "/no-type", ErrContentType, 0},
		{"page xhtml", page, "/xhtml", nil, 7},
		{"page png", page, "/png", ErrContentType, 0},
		{"image html", img, "/xhtml", ErrContentType, 0},
		{"image png", img, "/png", nil, 4},
		{"page at cap", page, "/page-max", nil, MaxPageBytes},
		{"page over cap CL", page, "/page-over", ErrTooLarge, 0},
		{"page over cap chunked", page, "/page-over?cl=0", ErrTooLarge, 0},
		{"page at cap chunked", page, "/page-max?cl=0", nil, MaxPageBytes},
		{"image at cap", img, "/img-max", nil, MaxImageBytes},
		{"image over cap CL", img, "/img-over", ErrTooLarge, 0},
		{"image over cap chunked", img, "/img-over?cl=0", ErrTooLarge, 0},
		{"image rejects html", img, "/page-over", ErrContentType, 0},
	} {
		resp, err := tc.fetch(ctx, e.url(tc.path))
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			} else if len(resp.Body) != tc.n {
				t.Errorf("%s: body %d bytes, want %d", tc.name, len(resp.Body), tc.n)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if resp == nil || resp.Body != nil || resp.StatusCode != 200 {
			t.Errorf("%s: want metadata-only response for logging, got %+v", tc.name, resp)
		}
	}
}

func TestHTTPStatus(t *testing.T) {
	e := newEnv(t)
	e.mux.HandleFunc("/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "go away")
	})
	c := e.client(nil)
	resp, err := c.FetchPage(context.Background(), e.url("/forbidden"))
	if !errors.Is(err, ErrHTTPStatus) {
		t.Fatalf("err = %v, want ErrHTTPStatus", err)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != 403 {
		t.Errorf("StatusError = %+v", se)
	}
	if resp == nil || resp.StatusCode != 403 || resp.FinalURL == nil || resp.Body != nil {
		t.Errorf("resp = %+v", resp)
	}
	// 404 from the mux (no handler).
	if _, err := c.FetchPage(context.Background(), e.url("/missing")); !errors.As(err, &se) || se.StatusCode != 404 {
		t.Errorf("missing: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // runs before srv.Close (LIFO)
	e.mux.HandleFunc("/slow-headers", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	e.mux.HandleFunc("/slow-body", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	c := e.client(func(cfg *Config) { cfg.Timeout = 200 * time.Millisecond })
	for _, p := range []string{"/slow-headers", "/slow-body"} {
		start := time.Now()
		_, err := c.FetchPage(context.Background(), e.url(p))
		if !errors.Is(err, ErrTimeout) {
			t.Errorf("%s: err = %v, want ErrTimeout", p, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", p, d)
		}
	}
	// A caller's shorter deadline also wins.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.client(nil).FetchPage(ctx, e.url("/slow-headers")); !errors.Is(err, ErrTimeout) {
		t.Errorf("ctx deadline: err = %v, want ErrTimeout", err)
	}
}

func TestRequestHygiene(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var seen []http.Header
	record := func(r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
	}
	e.mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		http.Redirect(w, r, "/end", http.StatusFound)
	})
	e.mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		html(w, r)
	})
	e.mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "image/webp")
		w.Write([]byte("RIFF"))
	})
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	c := e.client(nil)
	if c.transport.Proxy != nil {
		t.Error("transport must not use a proxy")
	}
	// Credentials in the URL must not become an Authorization header.
	withCreds := "http://user:secret@allowed.test:" + e.port + "/start"
	if _, err := c.FetchPage(context.Background(), withCreds); err != nil {
		t.Fatal(err) // would fail if HTTP_PROXY were honored
	}
	if _, err := c.FetchImage(context.Background(), e.url("/img")); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d requests, want 3", len(seen))
	}
	for i, h := range seen {
		if got := h.Get("User-Agent"); got != product.UserAgent() || !strings.HasPrefix(got, "KitchenGrimoire/") {
			t.Errorf("req %d: User-Agent = %q", i, got)
		}
		for _, k := range []string{"Cookie", "Authorization", "Referer", "Proxy-Authorization"} {
			if v := h.Values(k); len(v) > 0 {
				t.Errorf("req %d: unexpected %s: %q", i, k, v)
			}
		}
	}
	if a := seen[0].Get("Accept"); !strings.Contains(a, "text/html") {
		t.Errorf("page Accept = %q", a)
	}
	if a := seen[2].Get("Accept"); a != "image/*" {
		t.Errorf("image Accept = %q", a)
	}
}

// TestDNSRebinding: rebind.test first resolves to a public-looking address,
// then to 127.0.0.1. The first connection is allowed (a DialContext
// override stands in for the public server by connecting to our test
// server); the page then redirects back to the same hostname, a fresh
// connection is needed (the server closes the first), the second lookup
// returns loopback, and the dial is refused. This shows the check runs on
// every dialed address, not once per URL.
func TestDNSRebinding(t *testing.T) {
	e := newEnv(t)
	e.mux.HandleFunc("/first", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		http.Redirect(w, r, "/second", http.StatusFound)
	})
	e.mux.HandleFunc("/second", html)
	res := newStub(map[string][]string{"rebind.test": {"93.184.216.34", "127.0.0.1"}})
	var dialed []string
	c := New(Config{
		Resolver: res,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			var d net.Dialer
			return d.DialContext(ctx, network, e.srv.Listener.Addr().String())
		},
	})
	_, err := c.FetchPage(context.Background(), "http://rebind.test/first")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:80" {
		t.Errorf("dialed = %v, want only the public address", dialed)
	}
	if res.calls["rebind.test"] != 2 {
		t.Errorf("resolver called %d times, want 2", res.calls["rebind.test"])
	}
}

// TestMixedAnswers: if any resolved address is denied the host is refused,
// so an attacker cannot hide a private address behind a public one.
func TestMixedAnswers(t *testing.T) {
	res := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, nil
	})
	c := New(Config{Resolver: res, DialContext: noDial(t)})
	if _, err := c.FetchPage(context.Background(), "https://mixed.test/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, n, h string) ([]netip.Addr, error) {
	return f(ctx, n, h)
}

func TestExtraDenyAtDial(t *testing.T) {
	c := New(Config{
		ExtraDeny:   []netip.Prefix{netip.MustParsePrefix("93.184.216.0/24")},
		Resolver:    newStub(map[string][]string{"cluster.test": {"93.184.216.34"}}),
		DialContext: noDial(t),
	})
	if _, err := c.FetchPage(context.Background(), "http://cluster.test/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}

func TestDefaults(t *testing.T) {
	c := New(Config{})
	if c.timeout != DefaultTimeout || c.http.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v / %v", c.timeout, c.http.Timeout)
	}
	if c.maxRedir != DefaultMaxRedirects {
		t.Errorf("maxRedir = %d", c.maxRedir)
	}
	if c.userAgent != product.UserAgent() {
		t.Errorf("userAgent = %q", c.userAgent)
	}
	if c.http.Jar != nil || c.transport.Proxy != nil {
		t.Error("no cookie jar and no proxy expected")
	}
}
