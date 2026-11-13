package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAPI returns a minimal API with an empty config (no valid credentials).
func newTestAPI() *API {
	return &API{
		conf: &config.ConfigSettings{},
	}
}

// TestLoginBodyCap verifies that a request body exceeding 1 KiB returns 400.
func TestLoginBodyCap(t *testing.T) {
	a := newTestAPI()

	// Build a body that is clearly over 1 KiB.
	big := `{"username":"` + strings.Repeat("a", 5000) + `","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(big))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	a.Login(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code, "oversized body should return 400")
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
