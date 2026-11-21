// SPDX-License-Identifier: Apache-2.0

// Package config loads server configuration from environment variables
// (docs/DESIGN.md §14.1).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

// AuthMode selects how requests are authenticated (§12).
type AuthMode string

const (
	AuthTrustedHeader AuthMode = "trusted-header"
	AuthNone          AuthMode = "none"
)

// Config is the fully validated server configuration.
type Config struct {
	Listen      string
	AdminListen string
	DataDir     string
	BaseURL     *url.URL

	AuthMode       AuthMode
	TrustedProxies []netip.Prefix
	UserHeader     string
	NameHeader     string
	EmailHeader    string
	GroupsHeader   string
	Admins         []string
	AdminGroups    []string
	Dev            bool

	FetchTimeout    time.Duration
	FetchAllowHosts []string
	FetchDeny       []netip.Prefix // extra ranges the fetcher refuses (cluster CIDRs)
	TrashDays       int

	LogLevel slog.Level
}

// Load reads the configuration using getenv (normally os.Getenv; tests pass
// a map lookup). It returns every problem it finds, joined, rather than
// stopping at the first one, so a misconfigured deployment is fixed in one go.
func Load(getenv func(string) string) (*Config, error) {
	env := func(name, def string) string {
		if v := strings.TrimSpace(getenv(product.EnvPrefix + name)); v != "" {
			return v
		}
		return def
	}

	var errs []error
	bad := func(name string, err error) {
		errs = append(errs, fmt.Errorf("%s%s: %w", product.EnvPrefix, name, err))
	}

	c := &Config{
		Listen:          env("LISTEN", ":8080"),
		AdminListen:     env("ADMIN_LISTEN", ":9090"),
		DataDir:         env("DATA_DIR", "/data"),
		AuthMode:        AuthMode(env("AUTH_MODE", string(AuthTrustedHeader))),
		UserHeader:      env("USER_HEADER", "Remote-User"),
		NameHeader:      env("NAME_HEADER", "Remote-Name"),
		EmailHeader:     env("EMAIL_HEADER", "Remote-Email"),
		GroupsHeader:    env("GROUPS_HEADER", "Remote-Groups"),
		Admins:          splitList(env("ADMINS", "")),
		AdminGroups:     splitList(env("ADMIN_GROUPS", "")),
		FetchAllowHosts: splitList(env("FETCH_ALLOW_HOSTS", "")),
	}

	if raw := env("DEV", ""); raw != "" {
		dev, err := strconv.ParseBool(raw)
		if err != nil {
			bad("DEV", err)
		}
		c.Dev = dev
	}

	if raw := env("BASE_URL", ""); raw == "" {
		bad("BASE_URL", errors.New("is required"))
	} else if u, err := url.Parse(raw); err != nil {
		bad("BASE_URL", err)
	} else if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		bad("BASE_URL", fmt.Errorf("must be an absolute http(s) URL, got %q", raw))
	} else {
		u.Path = strings.TrimSuffix(u.Path, "/")
		c.BaseURL = u
	}

	switch c.AuthMode {
	case AuthTrustedHeader:
		raw := env("TRUSTED_PROXIES", "")
		if raw == "" {
			bad("TRUSTED_PROXIES", errors.New("is required when AUTH_MODE=trusted-header"))
		}
		for _, s := range splitList(raw) {
			p, err := parsePrefix(s)
			if err != nil {
				bad("TRUSTED_PROXIES", err)
				continue
			}
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	case AuthNone:
		if !c.Dev {
			bad("AUTH_MODE", errors.New("none is only allowed with GRIMOIRE_DEV=1"))
		}
	default:
		bad("AUTH_MODE", fmt.Errorf("unknown mode %q (want trusted-header or none)", c.AuthMode))
	}

	for _, s := range splitList(env("FETCH_DENY_CIDRS", "")) {
		p, err := parsePrefix(s)
		if err != nil {
			bad("FETCH_DENY_CIDRS", err)
			continue
		}
		c.FetchDeny = append(c.FetchDeny, p)
	}

	var err error
	if c.FetchTimeout, err = time.ParseDuration(env("FETCH_TIMEOUT", "15s")); err != nil {
		bad("FETCH_TIMEOUT", err)
	} else if c.FetchTimeout <= 0 {
		bad("FETCH_TIMEOUT", errors.New("must be positive"))
	}

	if c.TrashDays, err = strconv.Atoi(env("TRASH_DAYS", "30")); err != nil {
		bad("TRASH_DAYS", err)
	} else if c.TrashDays < 1 {
		bad("TRASH_DAYS", errors.New("must be at least 1"))
	}

	if err := c.LogLevel.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		bad("LOG_LEVEL", err)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// splitList splits a comma-separated value, trimming spaces and dropping
// empty entries.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parsePrefix accepts a CIDR ("10.0.0.0/8") or a bare address, which is
// treated as a single-host prefix.
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}
