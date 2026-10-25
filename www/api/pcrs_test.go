package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

func withEasyPCR(t *testing.T, enabled bool) *API {
	t.Helper()
	return &API{conf: &config.ConfigSettings{MiscSettings: config.MiscConfig{EasyPCR: enabled}}}
}

// allowPCRForTeam admits an admin for any team and a team user only for their
// own, and says which refusal applies.
func TestAllowPCRForTeam(t *testing.T) {
	a := withEasyPCR(t, true)

	onTeam1 := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 1, HasTeam: true}
	noTeam := Identity{Username: "injectmgr"}

	cases := []struct {
		name     string
		roles    []string
		identity Identity
		teamID   uint
		allowed  bool
		wantErr  string
	}{
		{"team user, own team", []string{"team"}, onTeam1, 1, true, ""},
		{"team user, other team", []string{"team"}, onTeam1, 2, false, "PCR not allowed"},
		{"team user, no team", []string{"team"}, noTeam, 1, false, "Your account is not associated with a team"},
		{"admin, any team", []string{"admin"}, noTeam, 999, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := a.allowPCRForTeam(w, requestAs(tc.roles, tc.identity), tc.teamID, "PCR not allowed")
			assert.Equal(t, tc.allowed, got)
			if tc.allowed {
				return
			}
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.JSONEq(t, `{"error":"`+tc.wantErr+`"}`, w.Body.String())
		})
	}
}

// EasyPCR off refuses a team user with the caller-supplied message.
func TestAllowPCRForTeamEasyPCRDisabled(t *testing.T) {
	a := withEasyPCR(t, false)

	onTeam1 := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 1, HasTeam: true}
	w := httptest.NewRecorder()

	assert.False(t, a.allowPCRForTeam(w, requestAs([]string{"team"}, onTeam1), 1, "PCR reset not allowed"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"error":"PCR reset not allowed"}`, w.Body.String())
}

func TestPCRMutationsRequireJSONContentType(t *testing.T) {
	a := withEasyPCR(t, true)
	onTeam1 := Identity{Username: "team01", Roles: []string{"team"}, TeamID: 1, HasTeam: true}

	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=abc"} {
		t.Run("CreatePcr_"+contentType, func(t *testing.T) {
			req := requestAs([]string{"team"}, onTeam1)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			a.CreatePcr(w, req)
			assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
			assert.JSONEq(t, `{"error":"Content-Type must be application/json"}`, w.Body.String())
		})

		t.Run("ResetPcr_"+contentType, func(t *testing.T) {
			req := requestAs([]string{"team"}, onTeam1)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			a.ResetPcr(w, req)
			assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
			assert.JSONEq(t, `{"error":"Content-Type must be application/json"}`, w.Body.String())
		})
	}

	t.Run("application/json with charset passes content-type check", func(t *testing.T) {
		req := requestAs([]string{"team"}, onTeam1)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Body = http.NoBody
		w := httptest.NewRecorder()
		assert.True(t, requireJSONContentType(w, req))
	})
}

func TestAdminOnlyPcrReadHandlersRejectTeamRole(t *testing.T) {
	a := withEasyPCR(t, true)
	onTeam1 := Identity{Username: "team01", Roles: []string{"team"}, TeamID: 1, HasTeam: true}

	t.Run("GetPcrs forbids non-admin", func(t *testing.T) {
		req := requestAs([]string{"team"}, onTeam1)
		w := httptest.NewRecorder()
		a.GetPcrs(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("GetPcrHistory forbids non-admin", func(t *testing.T) {
		req := requestAs([]string{"team"}, onTeam1)
		w := httptest.NewRecorder()
		a.GetPcrHistory(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}
