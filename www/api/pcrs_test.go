package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

// allowPCRForTeam compares team IDs as numbers, so a zero-padded ID names the
// same team as its bare form for an admin and a team user alike.
func TestAllowPCRForTeamComparesNumerically(t *testing.T) {
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = &config.ConfigSettings{MiscSettings: config.MiscConfig{EasyPCR: true}}

	onTeam1 := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 1, HasTeam: true}
	noTeam := Identity{Username: "admin", Roles: []string{"admin"}}

	cases := []struct {
		name     string
		roles    []string
		identity Identity
		teamID   uint
		allowed  bool
	}{
		{"team user, own team", []string{"team"}, onTeam1, 1, true},
		{"team user, other team", []string{"team"}, onTeam1, 2, false},
		{"team user, no such team", []string{"team"}, onTeam1, 999, false},
		{"admin, any team", []string{"admin"}, noTeam, 999, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := allowPCRForTeam(w, requestAs(tc.roles, tc.identity), tc.teamID, "PCR not allowed")
			assert.Equal(t, tc.allowed, got)
			if !tc.allowed {
				assert.Equal(t, http.StatusForbidden, w.Code)
			}
		})
	}
}

// A team user with no team is refused before any team ID is compared.
func TestAllowPCRForTeamWithoutTeam(t *testing.T) {
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = &config.ConfigSettings{MiscSettings: config.MiscConfig{EasyPCR: true}}

	w := httptest.NewRecorder()
	got := allowPCRForTeam(w, requestAs([]string{"team"}, Identity{Username: "injectmgr"}), 1, "PCR not allowed")
	assert.False(t, got)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"error":"Your account is not associated with a team"}`, w.Body.String())
}
