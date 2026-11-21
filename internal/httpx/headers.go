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
