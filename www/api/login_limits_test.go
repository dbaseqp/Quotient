package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAPI returns a minimal API with an empty config (no valid credentials).
func newTestAPI() *API {
	return &API{
		conf: config.NewLive(&config.ConfigSettings{}),
	}
}

// TestLoginBodyCap verifies that a request body over loginBodyLimit returns 413.
func TestLoginBodyCap(t *testing.T) {
	a := newTestAPI()

	// Build a body over the limit.
	big := `{"username":"` + strings.Repeat("a", 5000) + `","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(big))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	a.Login(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code, "oversized body should return 413")
}

// TestLoginRateLimit verifies that 11 failed attempts from the same IP returns 429.
func TestLoginRateLimit(t *testing.T) {
	a := newTestAPI()

	for i := 1; i <= 11; i++ {
		body := `{"username":"nobody","password":"wrong"}`
		req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:9999"

		rr := httptest.NewRecorder()
		a.Login(rr, req)

		if i <= 10 {
			assert.Equal(t, http.StatusUnauthorized, rr.Code, "attempt %d should be 401", i)
		} else {
			require.Equal(t, http.StatusTooManyRequests, rr.Code, "11th attempt should be 429")
		}
	}
}

func loginFrom(a *API, ip, user, pass string) int {
	body := `{"username":"` + user + `","password":"` + pass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":9999"
	rr := httptest.NewRecorder()
	a.Login(rr, req)
	return rr.Code
}

// TestLoginRateLimitRefusesCorrectCredentials verifies that an address past its failed attempts is
// refused before its credentials are checked, so a correct password cannot get through.
func TestLoginRateLimitRefusesCorrectCredentials(t *testing.T) {
	a := &API{conf: config.NewLive(&config.ConfigSettings{Admin: []config.Admin{{Name: "admin", Pw: "right"}}})}

	for i := 0; i < loginMaxAttempts; i++ {
		require.Equal(t, http.StatusUnauthorized, loginFrom(a, "10.0.0.2", "admin", "wrong"))
	}
	assert.Equal(t, http.StatusTooManyRequests, loginFrom(a, "10.0.0.2", "admin", "right"), "correct password must be refused once the limit is reached")
	assert.Equal(t, http.StatusUnauthorized, loginFrom(a, "10.0.0.3", "admin", "wrong"), "another address is not limited")
}

// TestLoginRateLimitWindowExpires verifies that the failure count starts over once the window has passed.
func TestLoginRateLimitWindowExpires(t *testing.T) {
	a := newTestAPI()
	for i := 0; i < loginMaxAttempts; i++ {
		require.Equal(t, http.StatusUnauthorized, loginFrom(a, "10.0.0.4", "nobody", "wrong"))
	}
	require.Equal(t, http.StatusTooManyRequests, loginFrom(a, "10.0.0.4", "nobody", "wrong"))

	val, ok := a.loginLimiter.Load("10.0.0.4")
	require.True(t, ok)
	la := val.(*loginAttempts)
	la.mu.Lock()
	la.windowStart = time.Now().Add(-2 * loginWindow)
	la.mu.Unlock()

	assert.Equal(t, http.StatusUnauthorized, loginFrom(a, "10.0.0.4", "nobody", "wrong"), "a new window allows attempts again")
}
