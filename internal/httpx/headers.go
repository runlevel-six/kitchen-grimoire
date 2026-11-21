// SPDX-License-Identifier: Apache-2.0

package httpx

import "net/http"

// csp is the Content Security Policy from docs/DESIGN.md §15.2: only our
// own scripts, styles, and images; no inline scripts; no framing.
const csp = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// SecurityHeaders sets the security headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Permissions-Policy", "camera=(self), geolocation=(), microphone=()")
		next.ServeHTTP(w, r)
	})
}

// HSTS tells browsers to use HTTPS for this host from now on. Only enable
// it when the public address is https (the proxy terminates TLS).
//
// Note: no Cross-Origin-Opener-Policy header. The capture bookmarklet
// relies on the recipe site keeping a reference to the /capture window it
// opened (window.opener + postMessage); COOP would cut that link.
func HSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}
