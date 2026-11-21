// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// UserStore creates or refreshes a user record. internal/store implements it.
type UserStore interface {
	UpsertUser(ctx context.Context, remoteUser, displayName, email string) (recipe.User, error)
}

// AuthConfig configures the authentication middleware (docs/DESIGN.md §12).
type AuthConfig struct {
	// None disables authentication: every request is the fixed user "dev".
	// Only allowed with GRIMOIRE_DEV=1 (enforced by config).
	None bool
	// TrustedProxies are the only peers whose identity headers are believed.
	TrustedProxies []netip.Prefix
	UserHeader     string // e.g. Remote-User
	NameHeader     string // e.g. Remote-Name
	EmailHeader    string // e.g. Remote-Email
	// GroupsHeader carries the user's groups, comma-separated (Authelia's
	// Remote-Groups). Members of any AdminGroups group are admins.
	GroupsHeader string
	Admins       []string
	AdminGroups  []string
	// Public paths need no identity: health checks and static assets.
	// Matched as exact paths or, when ending in "/", as prefixes.
	Public []string
}

// DevUser is the remote user name used when authentication is off.
const DevUser = "dev"

// maxHeaderValue bounds identity header values; real ones are short.
const maxHeaderValue = 256

// touchEvery limits how often a returning user's last-seen time is written.
const touchEvery = 5 * time.Minute

type ctxKey int

const userKey ctxKey = 1

// UserFrom returns the authenticated user, if any.
func UserFrom(ctx context.Context) (recipe.User, bool) {
	u, ok := ctx.Value(userKey).(recipe.User)
	return u, ok
}

// WithUser returns ctx carrying u (used by tests and the token API).
func WithUser(ctx context.Context, u recipe.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// Auth is the authentication middleware.
type Auth struct {
	cfg         AuthConfig
	users       UserStore
	log         *slog.Logger
	admins      map[string]bool
	adminGroups map[string]bool

	mu   sync.Mutex
	seen map[string]cachedUser // by remote user + name + email
}

type cachedUser struct {
	user recipe.User
	at   time.Time
}

// NewAuth returns the middleware.
func NewAuth(cfg AuthConfig, users UserStore, log *slog.Logger) *Auth {
	a := &Auth{cfg: cfg, users: users, log: log, admins: map[string]bool{}, seen: map[string]cachedUser{}}
	for _, name := range cfg.Admins {
		a.admins[strings.ToLower(name)] = true
	}
	a.adminGroups = map[string]bool{}
	for _, g := range cfg.AdminGroups {
		a.adminGroups[strings.ToLower(g)] = true
	}
	return a
}

// Middleware authenticates every non-public request and stores the user in
// the request context. Failures get 401 with no detail; the reason is
// logged.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		remote, name, email, reason := a.identity(r)
		if reason != "" {
			a.log.WarnContext(r.Context(), "unauthenticated request", "reason", reason,
				"peer", r.RemoteAddr, "path", r.URL.Path)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		u, err := a.lookup(r.Context(), remote, name, email)
		if err != nil {
			a.log.ErrorContext(r.Context(), "user lookup failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Group membership is read per request (it isn't stored), and only
		// after identity() has established that the peer is the proxy.
		if !u.IsAdmin && !a.cfg.None && a.cfg.GroupsHeader != "" {
			for _, g := range strings.Split(r.Header.Get(a.cfg.GroupsHeader), ",") {
				if a.adminGroups[strings.ToLower(strings.TrimSpace(g))] {
					u.IsAdmin = true
					break
				}
			}
		}
		noteUser(r.Context(), u.RemoteUser)
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func (a *Auth) isPublic(path string) bool {
	for _, p := range a.cfg.Public {
		if path == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(path, p)) {
			return true
		}
	}
	return false
}

// identity returns the remote user, name, and email, or a reason why the
// request is not authenticated.
func (a *Auth) identity(r *http.Request) (remote, name, email, reason string) {
	if a.cfg.None {
		return DevUser, "Developer", "", ""
	}
	peer, err := peerAddr(r.RemoteAddr)
	if err != nil {
		return "", "", "", "unparseable peer address"
	}
	if !a.trusted(peer) {
		// Never believe identity headers from anyone but the proxy: a
		// client talking to the pod directly could otherwise claim to be
		// anyone.
		return "", "", "", "peer is not a trusted proxy"
	}
	remote = strings.TrimSpace(r.Header.Get(a.cfg.UserHeader))
	if remote == "" {
		return "", "", "", "missing " + a.cfg.UserHeader + " header"
	}
	if !validHeaderValue(remote) {
		return "", "", "", "invalid " + a.cfg.UserHeader + " header"
	}
	name = strings.TrimSpace(r.Header.Get(a.cfg.NameHeader))
	email = strings.TrimSpace(r.Header.Get(a.cfg.EmailHeader))
	if !validHeaderValue(name) {
		name = ""
	}
	if !validHeaderValue(email) {
		email = ""
	}
	return remote, name, email, ""
}

func (a *Auth) trusted(peer netip.Addr) bool {
	for _, p := range a.cfg.TrustedProxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

// peerAddr parses "ip:port" from RemoteAddr. IPv4-mapped IPv6 addresses
// ("::ffff:10.0.0.1") are unmapped so they match IPv4 prefixes.
func peerAddr(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr.Unmap(), nil
}

func validHeaderValue(s string) bool {
	if len(s) > maxHeaderValue {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// lookup returns the user, creating them on first sight. Returning users
// are served from a small cache and their last-seen time is refreshed at
// most every touchEvery.
func (a *Auth) lookup(ctx context.Context, remote, name, email string) (recipe.User, error) {
	key := remote + "\x00" + name + "\x00" + email
	now := time.Now()
	a.mu.Lock()
	c, ok := a.seen[key]
	a.mu.Unlock()
	if !ok || now.Sub(c.at) > touchEvery {
		u, err := a.users.UpsertUser(ctx, remote, name, email)
		if err != nil {
			return recipe.User{}, err
		}
		c = cachedUser{user: u, at: now}
		a.mu.Lock()
		if len(a.seen) > 1000 { // a household is small; never grow without bound
			a.seen = map[string]cachedUser{}
		}
		a.seen[key] = c
		a.mu.Unlock()
	}
	u := c.user
	u.IsAdmin = a.cfg.None || a.admins[strings.ToLower(u.RemoteUser)]
	return u, nil
}
