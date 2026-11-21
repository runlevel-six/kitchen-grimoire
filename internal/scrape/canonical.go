// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// trackingParams are query parameters that identify a visit, not a page.
// Anything starting with "utm_" is also dropped.
var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "yclid": true, "twclid": true, "igshid": true, "li_fat_id": true,
	"mc_cid": true, "mc_eid": true, "_ga": true, "_gl": true, "_hsenc": true, "_hsmi": true,
	"ref": true, "ref_src": true, "ref_url": true, "s_cid": true, "cmpid": true,
	"mkt_tok": true, "oly_anon_id": true, "oly_enc_id": true, "vero_id": true, "spm": true,
}

func isTracking(key string) bool {
	key = strings.ToLower(key)
	return strings.HasPrefix(key, "utm_") || trackingParams[key]
}

// CanonicalURL returns the dedupe key for a fetched page (§8.2 step 10):
// the page's <link rel=canonical> resolved against finalURL if it is an
// absolute http(s) address, otherwise finalURL itself, in both cases with
// tracking parameters and the fragment removed. It returns "" when neither
// is usable.
func CanonicalURL(page []byte, finalURL *url.URL) string {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return canonicalURL(scanTokens(page).canonical, finalURL)
	}
	return canonicalURL(scanPage(doc).canonical, finalURL)
}

func canonicalURL(href string, final *url.URL) string {
	if href != "" {
		if u := resolve(final, href); u != nil {
			if s := CleanURL(u); s != "" {
				return s
			}
		}
	}
	if final == nil {
		return ""
	}
	return CleanURL(final)
}

// resolve parses ref relative to base (which may be nil) and returns it
// only if the result is an absolute http(s) URL with a host.
func resolve(base *url.URL, ref string) *url.URL {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > recipe.MaxURL {
		return nil
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if !httpURL(u) {
		return nil
	}
	return u
}

func httpURL(u *url.URL) bool {
	if u == nil || u.Host == "" || u.Hostname() == "" {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}

// CleanURL renders u without its fragment, user info, default port, or
// tracking parameters, with the scheme and host lowercased. Other query
// parameters keep their order and encoding. It returns "" if u is not an
// http(s) URL or the result is longer than recipe.MaxURL.
func CleanURL(u *url.URL) string {
	if !httpURL(u) {
		return ""
	}
	c := *u
	c.Scheme = strings.ToLower(c.Scheme)
	c.Host = strings.ToLower(c.Host)
	c.User = nil
	c.Fragment, c.RawFragment = "", ""
	if (c.Scheme == "http" && c.Port() == "80") || (c.Scheme == "https" && c.Port() == "443") {
		c.Host = c.Hostname()
		if strings.Contains(c.Host, ":") { // IPv6 literal
			c.Host = "[" + c.Host + "]"
		}
	}
	if c.Path == "" {
		c.Path, c.RawPath = "/", ""
	}
	if c.RawQuery != "" {
		var keep []string
		for _, pair := range strings.Split(c.RawQuery, "&") {
			if pair == "" {
				continue
			}
			k, _, _ := strings.Cut(pair, "=")
			if uk, err := url.QueryUnescape(k); err == nil {
				k = uk
			}
			if !isTracking(k) {
				keep = append(keep, pair)
			}
		}
		c.RawQuery = strings.Join(keep, "&")
	}
	c.ForceQuery = false
	s := c.String()
	if len(s) > recipe.MaxURL {
		return ""
	}
	return s
}
