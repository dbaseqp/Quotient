package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Deactivating a team or disabling one of its checks stops it being scored,
// so these admin POSTs must refuse CORS-simple content types (CSRF).
func TestAdminTeamUpdatesRequireJSONContentType(t *testing.T) {
	// No engine: the handlers would panic on it if they got past the check.
	a := withEasyPCR(t, true)
	admin := Identity{Username: "admin", Roles: []string{"admin"}}
	handlers := []struct {
		name   string
		handle http.HandlerFunc
	}{
		{"UpdateTeams", a.UpdateTeams},
		{"UpdateTeamChecks", a.UpdateTeamChecks},
	}

	for _, handler := range handlers {
		for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=abc"} {
			t.Run(handler.name+"_"+contentType, func(t *testing.T) {
				req := requestAs([]string{"admin"}, admin)
				req.Header.Set("Content-Type", contentType)
				w := httptest.NewRecorder()
				handler.handle(w, req)
				assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
			})
		}
	}
}
