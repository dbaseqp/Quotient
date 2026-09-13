package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

func withEasyPCR(t *testing.T, enabled bool) {
	t.Helper()
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = &config.ConfigSettings{MiscSettings: config.MiscConfig{EasyPCR: enabled}}
}

// allowPCRForTeam admits an admin for any team and a team user only for their
// own, and says which refusal applies.
func TestAllowPCRForTeam(t *testing.T) {
	withEasyPCR(t, true)

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
			got := allowPCRForTeam(w, requestAs(tc.roles, tc.identity), tc.teamID, "PCR not allowed")
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
	withEasyPCR(t, false)

	onTeam1 := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 1, HasTeam: true}
	w := httptest.NewRecorder()

	assert.False(t, allowPCRForTeam(w, requestAs([]string{"team"}, onTeam1), 1, "PCR reset not allowed"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"error":"PCR reset not allowed"}`, w.Body.String())
}
