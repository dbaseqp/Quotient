package middleware

import (
	"net/http"
	"quotient/engine/config"
)

// SecurityHeaders adds essential security headers to all responses
func SecurityHeaders(conf *config.ConfigSettings) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// HSTS: Force HTTPS for 1 year, include subdomains
			// This prevents http:// to https:// downgrade attacks
			// Only set if HTTPS is configured
			if conf.SslSettings != (config.SslConfig{}) {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			// X-Frame-Options: Prevent clickjacking
			w.Header().Set("X-Frame-Options", "DENY")

			// X-Content-Type-Options: Prevent MIME confusion attacks
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// X-XSS-Protection: Enable browser XSS filter (legacy but harmless)
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Content-Security-Policy: Restrict resource loading
			// Start with a restrictive policy - adjust as needed for your app
			csp := "default-src 'self'; " +
				"script-src 'self'; " +
				"style-src 'self' 'unsafe-inline'; " + // unsafe-inline often needed for CSS
				"img-src 'self' data:; " +
				"font-src 'self'; " +
				"connect-src 'self'; " +
				"frame-ancestors 'none'" // Redundant with X-Frame-Options but good defense-in-depth
			w.Header().Set("Content-Security-Policy", csp)

			// Referrer-Policy: Control referer header leakage
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// Permissions-Policy: Disable unnecessary browser features
			w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

			next(w, r)
		}
	}
}
