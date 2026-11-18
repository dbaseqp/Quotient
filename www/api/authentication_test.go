package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestLogoutMethodRestriction verifies that only POST is accepted on /api/logout.
func TestLogoutMethodRestriction(t *testing.T) {
	mux := http.NewServeMux()
	a := &API{conf: &config.ConfigSettings{}}

	// Register only POST, matching www/router.go.
	mux.HandleFunc("POST /api/logout", a.Logout)

	t.Run("GET returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/logout", nil)
		req = req.WithContext(WithIdentity(context.Background(), Identity{Username: "u", Roles: []string{"team"}}))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("POST clears session cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
		req = req.WithContext(WithIdentity(context.Background(), Identity{Username: "u", Roles: []string{"team"}}))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		var found *http.Cookie
		for _, c := range rr.Result().Cookies() {
			if c.Name == COOKIENAME {
				found = c
				break
			}
		}
		require.NotNil(t, found, "logout must set the session cookie")
		// MaxAge -1 tells the browser to delete the cookie immediately.
		assert.Equal(t, -1, found.MaxAge, "cookie must have MaxAge=-1 to force deletion")
	})
}

// loginCookie returns the session cookie Login sets for user/pw.
func loginCookie(t *testing.T, a *API, user, pw string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	body := `{"username":"` + user + `","password":"` + pw + `"}`
	a.Login(rec, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, c := range rec.Result().Cookies() {
		if c.Name == COOKIENAME {
			return c
		}
	}
	t.Fatal("Login set no session cookie")
	return nil
}

// authenticateWith runs Authenticate on a request carrying cookie.
func authenticateWith(a *API, cookie *http.Cookie) (Identity, bool) {
	req := httptest.NewRequest(http.MethodGet, "/api/engine", nil)
	req.AddCookie(cookie)
	return a.Authenticate(httptest.NewRecorder(), req)
}

// Rotating a leaked local password must end the sessions issued under it.
func TestPasswordChangeInvalidatesLocalSession(t *testing.T) {
	conf := &config.ConfigSettings{Admin: []config.Admin{
		{Name: "admin", Pw: "leaked-password"},
		{Name: "other", Pw: "other-password"},
	}}
	a := NewAPI(conf, nil)
	stolen := loginCookie(t, a, "admin", "leaked-password")
	untouched := loginCookie(t, a, "other", "other-password")

	id, ok := authenticateWith(a, stolen)
	require.True(t, ok, "control: a fresh cookie authenticates")
	require.Equal(t, []string{"admin"}, id.Roles)

	conf.Admin[0].Pw = "new-password"

	id, ok = authenticateWith(a, stolen)
	assert.False(t, ok, "cookie issued before the password change still authenticates as %q with roles %v", id.Username, id.Roles)

	_, ok = authenticateWith(a, untouched)
	assert.True(t, ok, "another account's session must survive the change")

	_, ok = authenticateWith(a, loginCookie(t, a, "admin", "new-password"))
	assert.True(t, ok, "a cookie issued under the new password authenticates")
}

// A local session cookie issued without a credential fingerprint (before the
// fingerprint existed) is rejected, forcing one fresh login.
func TestLocalSessionWithoutCredentialRejected(t *testing.T) {
	a := NewAPI(&config.ConfigSettings{Admin: []config.Admin{{Name: "admin", Pw: "pw"}}}, nil)
	value, err := CookieEncoder.Encode(COOKIENAME, map[string]any{"username": "admin", "authSource": "local"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/engine", nil)
	req.AddCookie(&http.Cookie{Name: COOKIENAME, Value: value})
	rec := httptest.NewRecorder()
	_, ok := a.Authenticate(rec, req)
	assert.False(t, ok)

	var cleared bool
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == COOKIENAME && c.MaxAge < 0)
	}
	assert.True(t, cleared, "the stale cookie is cleared")
}
