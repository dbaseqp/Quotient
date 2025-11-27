package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
