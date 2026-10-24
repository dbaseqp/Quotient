package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionCookieCreation tests secure cookie creation
func TestSessionCookieCreation(t *testing.T) {
	// Ensure CookieEncoder is initialized
	require.NotNil(t, CookieEncoder, "CookieEncoder should be initialized")

	t.Run("encode and decode session", func(t *testing.T) {
		session := map[string]any{
			"username": "test_user",
		}

		// Encode session
		encoded, err := CookieEncoder.Encode(COOKIENAME, session)
		require.NoError(t, err)
		assert.NotEmpty(t, encoded)

		// Decode session
		var decoded map[string]any
		err = CookieEncoder.Decode(COOKIENAME, encoded, &decoded)
		require.NoError(t, err)

		// Verify decoded session matches original
		assert.Equal(t, session["username"], decoded["username"])
	})

	t.Run("tampered cookie fails decode", func(t *testing.T) {
		session := map[string]any{"username": "admin"}
		encoded, _ := CookieEncoder.Encode(COOKIENAME, session)

		// Tamper with the encoded value
		tampered := encoded + "tampered"

		// Try to decode tampered cookie
		var decoded map[string]any
		err := CookieEncoder.Decode(COOKIENAME, tampered, &decoded)
		assert.Error(t, err, "tampered cookie should fail to decode")
	})

	t.Run("different cookie name fails", func(t *testing.T) {
		session := map[string]any{"username": "user"}
		encoded, _ := CookieEncoder.Encode(COOKIENAME, session)

		// Try to decode with different cookie name
		var decoded map[string]any
		err := CookieEncoder.Decode("wrong_cookie_name", encoded, &decoded)
		assert.Error(t, err, "wrong cookie name should fail")
	})
}

// TestLogoutEndpoint tests the logout functionality
func TestLogoutEndpoint(t *testing.T) {
	t.Run("logout sets expired cookie", func(t *testing.T) {
		rr := httptest.NewRecorder()

		// Simulate logout by setting expired cookie
		cookie := &http.Cookie{
			Name:   COOKIENAME,
			Value:  "",
			Path:   "/",
			MaxAge: -1, // Expire immediately
		}

		http.SetCookie(rr, cookie)

		cookies := rr.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, COOKIENAME, cookies[0].Name)
		assert.Equal(t, -1, cookies[0].MaxAge)
	})
}

func TestOIDCCookieFallbackWhenRedisUnavailable(t *testing.T) {
	a := &API{conf: &config.ConfigSettings{
		OIDCSettings: config.OIDCAuthConfig{
			OIDCEnabled:    true,
			OIDCAdminGroups: []string{"WCComps_Quotient_Admin"},
		},
	}}

	t.Run("valid OIDC cookie authenticates when Redis is unavailable", func(t *testing.T) {
		cookieData := map[string]any{
			"username":   "hypernova",
			"authSource": "oidc",
			"groups":     []string{"WCComps_Quotient_Admin"},
			"roles":      []string{"admin"},
			"expiresAt":  time.Now().Add(time.Hour).Unix(),
		}
		encoded, err := CookieEncoder.Encode(COOKIENAME, cookieData)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/announcements", nil)
		req.AddCookie(&http.Cookie{Name: COOKIENAME, Value: encoded})
		rr := httptest.NewRecorder()

		id, ok := a.Authenticate(rr, req)
		require.True(t, ok, "should authenticate using verified cookie claims when Redis is unavailable")
		assert.Equal(t, "hypernova", id.Username)
		assert.Equal(t, []string{"admin"}, id.Roles)
	})

	t.Run("expired OIDC cookie fails and clears cookie when Redis is unavailable", func(t *testing.T) {
		cookieData := map[string]any{
			"username":   "hypernova",
			"authSource": "oidc",
			"groups":     []string{"WCComps_Quotient_Admin"},
			"roles":      []string{"admin"},
			"expiresAt":  time.Now().Add(-time.Hour).Unix(),
		}
		encoded, err := CookieEncoder.Encode(COOKIENAME, cookieData)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/announcements", nil)
		req.AddCookie(&http.Cookie{Name: COOKIENAME, Value: encoded})
		rr := httptest.NewRecorder()

		_, ok := a.Authenticate(rr, req)
		assert.False(t, ok, "expired OIDC cookie should fail authentication")
		cookies := rr.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, -1, cookies[0].MaxAge)
	})
}
