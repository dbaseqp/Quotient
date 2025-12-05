package middleware

import (
	"net/http"
	"net/http/httptest"
	"quotient/engine/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityHeaders(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	t.Run("sets all security headers without SSL", func(t *testing.T) {
		conf := &config.ConfigSettings{
			SslSettings: config.SslConfig{}, // No SSL
		}

		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		headers := w.Result().Header

		// HSTS should NOT be set without SSL
		assert.Empty(t, headers.Get("Strict-Transport-Security"),
			"HSTS should not be set without SSL")

		// All other headers should be set
		assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
		assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
		assert.Equal(t, "1; mode=block", headers.Get("X-XSS-Protection"))
		assert.Contains(t, headers.Get("Content-Security-Policy"), "default-src 'self'")
		assert.Equal(t, "strict-origin-when-cross-origin", headers.Get("Referrer-Policy"))
		assert.Contains(t, headers.Get("Permissions-Policy"), "geolocation=()")
	})

	t.Run("sets HSTS header with SSL configured", func(t *testing.T) {
		conf := &config.ConfigSettings{
			SslSettings: config.SslConfig{
				HttpsCert: "/path/to/cert.pem",
				HttpsKey:  "/path/to/key.pem",
			},
		}

		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		hsts := w.Result().Header.Get("Strict-Transport-Security")
		assert.Equal(t, "max-age=31536000; includeSubDomains", hsts,
			"HSTS should be set with SSL configured")
	})

	t.Run("sets restrictive CSP", func(t *testing.T) {
		conf := &config.ConfigSettings{}
		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		csp := w.Result().Header.Get("Content-Security-Policy")

		// Verify restrictive directives
		assert.Contains(t, csp, "default-src 'self'")
		assert.Contains(t, csp, "script-src 'self'")
		assert.Contains(t, csp, "frame-ancestors 'none'")
	})

	t.Run("prevents clickjacking", func(t *testing.T) {
		conf := &config.ConfigSettings{}
		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		headers := w.Result().Header

		// Two layers of clickjacking protection
		assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
		assert.Contains(t, headers.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	})

	t.Run("prevents MIME confusion", func(t *testing.T) {
		conf := &config.ConfigSettings{}
		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		assert.Equal(t, "nosniff", w.Result().Header.Get("X-Content-Type-Options"))
	})

	t.Run("disables dangerous browser features", func(t *testing.T) {
		conf := &config.ConfigSettings{}
		middleware := SecurityHeaders(conf)
		wrapped := middleware(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		permissions := w.Result().Header.Get("Permissions-Policy")
		assert.Contains(t, permissions, "geolocation=()")
		assert.Contains(t, permissions, "microphone=()")
		assert.Contains(t, permissions, "camera=()")
	})
}
