// SPDX-License-Identifier: Apache-2.0

// Package safefetch is the only way the server fetches URLs that users give
// it (recipe pages and their images). It guards against server-side request
// forgery (SSRF): a user must not be able to make the server talk to
// localhost, the cluster network, a cloud metadata endpoint, or anything
// else that is not the public internet (DESIGN.md §15.1).
//
// The key idea: the destination check runs when the TCP connection is
// opened, against the IP address actually being dialed, not against the
// hostname in the URL. A check on the URL alone is easy to defeat: a
// hostname can resolve to 127.0.0.1, a DNS server can answer differently on
// the second lookup ("DNS rebinding"), and a public page can redirect to a
// private one. Because every connection (first request and every redirect)
// goes through our dialer, all of those are caught in one place.
//
// The client is deliberately polite and honest (§8.2): it identifies itself
// with product.UserAgent(), sends no cookies or credentials, does not use
// proxies, and never retries.
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

// Size limits for response bodies.
const (
	MaxPageBytes  = 5 << 20  // 5 MiB
	MaxImageBytes = 10 << 20 // 10 MiB
)

// Defaults applied by New for zero-valued Config fields.
const (
	DefaultTimeout      = 15 * time.Second
	DefaultMaxRedirects = 5
)

// Sentinel errors. Every error returned by FetchPage and FetchImage that is
// caused by one of these conditions wraps the matching sentinel, so callers
// test with errors.Is(err, safefetch.ErrBlocked) and so on. The wrapped
// message carries details (the address, the port, the status) for logging.
var (
	// ErrInvalidURL: the URL could not be parsed or has no host.
	ErrInvalidURL = errors.New("safefetch: invalid URL")
	// ErrScheme: the URL (or a redirect target) is not http or https.
	ErrScheme = errors.New("safefetch: scheme not allowed")
	// ErrPort: the URL (or a redirect target) uses a port other than 80 or 443.
	ErrPort = errors.New("safefetch: port not allowed")
	// ErrBlocked: the destination resolved to an address we refuse to
	// connect to (loopback, private, link-local, ...).
	ErrBlocked = errors.New("safefetch: destination address blocked")
	// ErrTooManyRedirects: more than Config.MaxRedirects redirects.
	ErrTooManyRedirects = errors.New("safefetch: too many redirects")
	// ErrHTTPStatus: the final response was not 2xx. See *StatusError.
	ErrHTTPStatus = errors.New("safefetch: unexpected HTTP status")
	// ErrContentType: the response Content-Type is not acceptable.
	ErrContentType = errors.New("safefetch: unexpected content type")
	// ErrTooLarge: the response body exceeds the size cap.
	ErrTooLarge = errors.New("safefetch: response body too large")
	// ErrTimeout: the total time limit (Config.Timeout or the caller's
	// context deadline) ran out.
	ErrTimeout = errors.New("safefetch: timed out")
)

// StatusError is returned (wrapped) for a non-2xx response. It matches
// ErrHTTPStatus under errors.Is; use errors.As to get the code.
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%v: %d %s", ErrHTTPStatus, e.StatusCode, http.StatusText(e.StatusCode))
}

// Is makes errors.Is(err, ErrHTTPStatus) true for a *StatusError.
func (e *StatusError) Is(target error) bool { return target == ErrHTTPStatus }

// Resolver looks up the IP addresses of a hostname. *net.Resolver
// satisfies it; tests pass a stub. network is "ip", "ip4" or "ip6".
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Config configures a Client. The zero value is usable: every field has a
// safe default.
type Config struct {
	// Timeout bounds the whole fetch: DNS, connect, TLS, redirects and
	// reading the body. Zero means DefaultTimeout (15s).
	Timeout time.Duration
	// MaxRedirects is how many redirects to follow. Zero means
	// DefaultMaxRedirects (5); a negative value follows none.
	MaxRedirects int
	// ExtraDeny lists more address ranges to refuse, typically the cluster
	// pod and service CIDRs.
	ExtraDeny []netip.Prefix
	// AllowHosts is a TEST-ONLY escape hatch (GRIMOIRE_FETCH_ALLOW_HOSTS):
	// URLs whose host matches an entry exactly (case-insensitive, no port)
	// may use any port and may connect to any address, including private
	// ones. Never set it in production.
	AllowHosts []string
	// UserAgent defaults to product.UserAgent(). Leave it alone outside
	// tests: §8.2 forbids pretending to be a browser.
	UserAgent string
	// Resolver defaults to net.DefaultResolver.
	Resolver Resolver
	// DialContext, if set, replaces the real network dial (tests use it to
	// observe or fake connections). It is called only after the address
	// check has passed, and always with a literal "ip:port" address, so it
	// cannot bypass the check.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Client fetches pages and images under the SSRF rules. It is safe for
// concurrent use; create one and share it.
type Client struct {
	http       *http.Client
	transport  *http.Transport
	timeout    time.Duration
	maxRedir   int
	extraDeny  []netip.Prefix
	allowHosts map[string]bool
	userAgent  string
	resolver   Resolver
	dialFn     func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Response is a successfully fetched resource, or, alongside an
// ErrHTTPStatus / ErrContentType / ErrTooLarge error, the metadata of the
// rejected one (with Body nil) so callers can log it.
type Response struct {
	FinalURL    *url.URL // URL after redirects
	StatusCode  int
	ContentType string // media type only, lower-case, without parameters
	Body        []byte
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	c := &Client{
		timeout:    cfg.Timeout,
		maxRedir:   cfg.MaxRedirects,
		extraDeny:  normalizePrefixes(cfg.ExtraDeny),
		allowHosts: make(map[string]bool, len(cfg.AllowHosts)),
		userAgent:  cfg.UserAgent,
		resolver:   cfg.Resolver,
		dialFn:     cfg.DialContext,
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	switch {
	case c.maxRedir == 0:
		c.maxRedir = DefaultMaxRedirects
	case c.maxRedir < 0:
		c.maxRedir = 0
	}
	for _, h := range cfg.AllowHosts {
		if h = normHost(h); h != "" {
			c.allowHosts[h] = true
		}
	}
	if c.userAgent == "" {
		c.userAgent = product.UserAgent()
	}
	if c.resolver == nil {
		c.resolver = net.DefaultResolver
	}

	c.transport = &http.Transport{
		// Explicitly no proxy: http.DefaultTransport would honor
		// HTTP_PROXY, which would both bypass our address check (we would
		// dial the proxy, not the target) and break §8.2.
		Proxy:       nil,
		DialContext: c.dialContext,
		// A custom DialContext turns HTTP/2 off unless we ask for it.
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
		MaxIdleConns:           16,
		IdleConnTimeout:        30 * time.Second,
	}
	c.http = &http.Client{
		Transport:     c.transport,
		Timeout:       c.timeout,
		CheckRedirect: c.checkRedirect,
		Jar:           nil, // no cookies, ever
	}
	return c
}

// FetchPage GETs an HTML page. It accepts text/html or
// application/xhtml+xml and caps the body at MaxPageBytes.
func (c *Client) FetchPage(ctx context.Context, rawURL string) (*Response, error) {
	return c.fetch(ctx, rawURL, kindPage)
}

// FetchImage GETs an image. It accepts any image/* type and caps the body
// at MaxImageBytes. The caller must still decode and re-encode it (§8.5):
// the Content-Type header is only the server's claim.
func (c *Client) FetchImage(ctx context.Context, rawURL string) (*Response, error) {
	return c.fetch(ctx, rawURL, kindImage)
}

type kind int

const (
	kindPage kind = iota
	kindImage
)

func (k kind) accept() string {
	if k == kindImage {
		return "image/*"
	}
	return "text/html,application/xhtml+xml;q=0.9"
}

func (k kind) maxBytes() int64 {
	if k == kindImage {
		return MaxImageBytes
	}
	return MaxPageBytes
}

func (k kind) acceptable(mediaType string) bool {
	if k == kindImage {
		return strings.HasPrefix(mediaType, "image/")
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func (c *Client) fetch(ctx context.Context, rawURL string, k kind) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	if err := c.checkURL(u); err != nil {
		return nil, err
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: %q has no host", ErrInvalidURL, rawURL)
	}

	// Two timeouts on purpose: the context deadline stops everything,
	// including our own DNS lookup in dialContext; http.Client.Timeout is
	// a backstop that also covers reading the body.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	// Only these headers are sent (plus Host and Go's Accept-Encoding:
	// gzip, which the transport decodes transparently). No Cookie, no
	// Authorization, no Referer.
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", k.accept())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.wrapErr(ctx, err)
	}
	defer resp.Body.Close()

	out := &Response{FinalURL: resp.Request.URL, StatusCode: resp.StatusCode}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
		out.ContentType = strings.ToLower(mt)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return out, &StatusError{StatusCode: resp.StatusCode}
	}
	// Check the type before reading a single byte of the body.
	if !k.acceptable(out.ContentType) {
		return out, fmt.Errorf("%w: got %q", ErrContentType, resp.Header.Get("Content-Type"))
	}
	limit := k.maxBytes()
	if resp.ContentLength > limit {
		return out, fmt.Errorf("%w: Content-Length %d exceeds %d", ErrTooLarge, resp.ContentLength, limit)
	}
	// Content-Length can be absent or wrong (and gzip decoding changes the
	// size), so enforce the cap while reading: read one byte more than
	// allowed, and if we got it the body was too big.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return out, c.wrapErr(ctx, err)
	}
	if int64(len(body)) > limit {
		return out, fmt.Errorf("%w: body exceeds %d bytes", ErrTooLarge, limit)
	}
	out.Body = body
	return out, nil
}

// wrapErr adds ErrTimeout to errors caused by running out of time, so
// callers do not need to know the several shapes a timeout can take.
func (c *Client) wrapErr(ctx context.Context, err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout()) {
		if !errors.Is(err, ErrBlocked) {
			return fmt.Errorf("%w: %w", ErrTimeout, err)
		}
	}
	return err
}

// checkRedirect is called by http.Client before following each redirect.
// req is the new request; via holds the requests made so far.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > c.maxRedir {
		return fmt.Errorf("%w: limit is %d", ErrTooManyRedirects, c.maxRedir)
	}
	// net/http adds a Referer to redirected requests; we never send one.
	req.Header.Del("Referer")
	return c.checkURL(req.URL)
}

// checkURL enforces the scheme and port rules and strips credentials
// embedded in the URL (net/http would turn "user:pass@" into an
// Authorization header). The address rule is enforced later, at dial time.
func (c *Client) checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %q", ErrScheme, u.Scheme)
	}
	u.User = nil
	if c.allowHosts[normHost(u.Hostname())] {
		return nil
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return fmt.Errorf("%w: %s", ErrPort, p)
	}
	return nil
}

// dialContext is the Transport's dial function. addr is "host:port" from
// the URL. We resolve the host ourselves (so tests can stub DNS), refuse
// if ANY resolved address is denied, then dial the addresses one at a time
// with a net.Dialer whose Control hook re-checks the exact IP the socket
// is connecting to.
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	allowed := c.allowHosts[normHost(host)]
	if !allowed && portStr != "80" && portStr != "443" {
		return nil, fmt.Errorf("%w: %s", ErrPort, portStr)
	}

	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip} // literal IP in the URL; nothing to resolve
	} else {
		ips, err = c.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
		}
	}
	if !allowed {
		for _, ip := range ips {
			if deniedAddr(ip, c.extraDeny) {
				return nil, blockedErr(host, ip)
			}
		}
	}

	var firstErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.WithZone("").Unmap().String(), portStr)
		var conn net.Conn
		if c.dialFn != nil {
			conn, err = c.dialFn(ctx, network, target)
		} else {
			d := net.Dialer{Control: c.controlFunc(allowed)}
			conn, err = d.DialContext(ctx, network, target)
		}
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// controlFunc returns a net.Dialer Control hook. The kernel calls it after
// creating the socket and before connecting, with the literal "ip:port"
// being connected to. This is the last line of defense: whatever happened
// with DNS or redirects, a socket never connects to a denied address.
func (c *Client) controlFunc(allowed bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allowed {
			return nil
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: unparseable dial address %q", ErrBlocked, address)
		}
		if deniedAddr(ap.Addr(), c.extraDeny) {
			return blockedErr("", ap.Addr())
		}
		return nil
	}
}

func blockedErr(host string, ip netip.Addr) error {
	if host == "" || host == ip.String() {
		return fmt.Errorf("%w: %s", ErrBlocked, ip)
	}
	return fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip)
}

// normHost lower-cases a hostname and drops a trailing dot, so "Example.COM."
// and "example.com" compare equal.
func normHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}
