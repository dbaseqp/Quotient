package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"quotient/engine/config"
	"quotient/engine/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func teamList(names ...string) []db.TeamSchema {
	teams := make([]db.TeamSchema, 0, len(names))
	for i, name := range names {
		teams = append(teams, db.TeamSchema{ID: uint(i + 1), Name: name})
	}
	return teams
}

func withOIDCConfig(t *testing.T, teamGroups []string, groupMap map[string]string) {
	t.Helper()
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = &config.ConfigSettings{
		OIDCSettings: config.OIDCAuthConfig{
			OIDCEnabled:      true,
			OIDCTeamGroups:   teamGroups,
			OIDCTeamGroupMap: groupMap,
		},
	}
}

func TestTeamOrdinal(t *testing.T) {
	cases := []struct {
		name     string
		expected uint64
		ok       bool
	}{
		{"quotient-blue-Team-05", 5, true},
		{"quotient-blue-Team-5", 5, true},
		{"team05", 5, true},
		{"team5", 5, true},
		{"Team 5", 5, true},
		{"WCComps_Quotient_Blue_Team01", 1, true},
		{"team10", 10, true},
		{"team100", 100, true},
		{"redteam", 0, false},
		{"", 0, false},
		// A trailing letter is not an ordinal.
		{"quotient-blue-Team-05b", 0, false},
		{"ccdc-blue-alpha", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := teamOrdinal(tc.name)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// A prefix-matched group with a padded ordinal maps to a padded team name.
func TestMapOIDCUserToTeamPaddedOrdinal(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team02", "team03", "team04", "team05")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"})
	require.NotNil(t, team)
	assert.Equal(t, "team05", team.Name)
	assert.Equal(t, uint(5), team.ID)
}

func TestMapOIDCUserToTeamUnpaddedTeamNames(t *testing.T) {
	// event.conf.example names teams "team1", "team2" - unpadded.
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team1", "team2", "team3", "team4", "team5")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"})
	require.NotNil(t, team)
	assert.Equal(t, "team5", team.Name)
}

func TestMapOIDCUserToTeamSingleDigitGroup(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team05")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-5"})
	require.NotNil(t, team)
	assert.Equal(t, "team05", team.Name)
}

// A group or team name ending in a letter carries no ordinal, so pass 3 skips
// it. Such names need the explicit map or an exact name match.
func TestMapOIDCUserToTeamIgnoresLetterSuffixedNames(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team05a", "team05b")

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05b"}))
	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"}))
}

func TestMapOIDCUserToTeamExactNameMatch(t *testing.T) {
	withOIDCConfig(t, []string{"BlueAlpha", "BlueBravo"}, nil)
	teams := teamList("BlueAlpha", "BlueBravo")

	// The group must match a configured pattern as the role mapper matches it,
	// but the team Name comparison itself ignores case.
	team := mapOIDCUserToTeam(teams, []string{"BlueBravo"})
	require.NotNil(t, team)
	assert.Equal(t, "BlueBravo", team.Name)

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"bluebravo"}))
}

func TestMapOIDCUserToTeamExplicitMapWins(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, map[string]string{
		"quotient-blue-Team-05": "team12",
	})
	teams := teamList("team05", "team12")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"})
	require.NotNil(t, team)
	assert.Equal(t, "team12", team.Name)
}

func TestMapOIDCUserToTeamExplicitMapWorksWithoutTeamGroupPattern(t *testing.T) {
	// A group with no team ordinal resolves only via the explicit map.
	withOIDCConfig(t, []string{"ccdc-blue-*"}, map[string]string{
		"ccdc-blue-charlie": "team03",
	})
	teams := teamList("team01", "team02", "team03")

	team := mapOIDCUserToTeam(teams, []string{"ccdc-blue-charlie"})
	require.NotNil(t, team)
	assert.Equal(t, "team03", team.Name)
}

// A mapped group whose team does not exist must resolve to nothing, not fall
// through to the trailing-ordinal pass.
func TestMapOIDCUserToTeamRefusesBrokenMapEntry(t *testing.T) {
	withOIDCConfig(t, []string{"ccdc-blue-*"}, map[string]string{
		// The operator meant team01 and dropped the zero.
		"ccdc-blue-alpha-room3": "team1",
	})
	teams := teamList("team01", "team02", "team03")

	// The trailing 3 of "room3" would otherwise match team03.
	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"ccdc-blue-alpha-room3"}))
}

// The refusal is scoped to the mapped group; other groups still resolve.
func TestMapOIDCUserToTeamBrokenEntryDoesNotBlockUnmappedGroups(t *testing.T) {
	withOIDCConfig(t, []string{"ccdc-blue-*"}, map[string]string{
		"ccdc-blue-alpha-room3": "team1",
	})
	teams := teamList("team01", "team02", "team03")

	team := mapOIDCUserToTeam(teams, []string{"ccdc-blue-Team-02"})
	require.NotNil(t, team)
	assert.Equal(t, "team02", team.Name)
}

func TestMapOIDCUserToTeamRefusesAmbiguousMatch(t *testing.T) {
	// "team5" and "team05" both reduce to ordinal 5, so nothing resolves.
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team5", "team05")

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"}))
}

func TestMapOIDCUserToTeamIgnoresNonTeamGroups(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team05")

	// A group outside the configured patterns must not assign a team, even
	// though it ends in digits.
	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"vpn-users-05", "some-other-group"}))
}

func TestMapOIDCUserToTeamNoMatch(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team02")

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-99"}))
	assert.Nil(t, mapOIDCUserToTeam(teams, nil))
}

// The hyphen in "Team-5" separates the ordinal; it is not part of it.
func TestMapOIDCUserToTeamHyphenBeforeSingleDigit(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team05")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-5"})
	require.NotNil(t, team)
	assert.Equal(t, "team05", team.Name)
}

// Local and LDAP accounts are named after their team; the rule is a name match.
func TestTeamNamedMatchesTeamName(t *testing.T) {
	teams := teamList("team01", "team02")

	team, err := teamNamed("team02")(teams)
	require.NoError(t, err)
	assert.Equal(t, uint(2), team.ID)
}

func TestTeamNamedWithoutTeam(t *testing.T) {
	teams := teamList("team01", "team02")

	_, err := teamNamed("injectmanager")(teams)
	require.Error(t, err)
}

// A team ID must not be readable without the caller learning whether one
// exists.
func TestCallerTeamIDReportsAbsence(t *testing.T) {
	_, hasTeam := CallerTeamID(context.Background())
	assert.False(t, hasTeam, "a request with no identity must report no team")

	ctx := WithIdentity(context.Background(), Identity{Username: "injectmgr", Roles: []string{"inject"}})
	_, hasTeam = CallerTeamID(ctx)
	assert.False(t, hasTeam, "an identity with no team must report no team")

	ctx = WithIdentity(context.Background(), Identity{Username: "hola", Roles: []string{"team"}, TeamID: 5, HasTeam: true})
	id, hasTeam := CallerTeamID(ctx)
	assert.True(t, hasTeam)
	assert.Equal(t, uint(5), id)
}

// The context carries the identity the middleware stored.
func TestIdentityRoundTripsThroughContext(t *testing.T) {
	want := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 5, HasTeam: true}
	got, ok := IdentityFrom(WithIdentity(context.Background(), want))
	require.True(t, ok)
	assert.Equal(t, want, got)

	_, ok = IdentityFrom(context.Background())
	assert.False(t, ok)
}

func requestAs(roles []string, id Identity) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithIdentity(r.Context(), id)
	ctx = context.WithValue(ctx, "roles", roles)
	return r.WithContext(ctx)
}

func TestRequireOwnTeam(t *testing.T) {
	onTeam5 := Identity{Username: "hola", Roles: []string{"team"}, TeamID: 5, HasTeam: true}
	noTeam := Identity{Username: "injectmgr", Roles: []string{"inject"}}

	cases := []struct {
		name        string
		roles       []string
		identity    Identity
		teamID      uint
		bypassRoles []string
		allowed     bool
	}{
		{"own team", []string{"team"}, onTeam5, 5, []string{"admin"}, true},
		{"other team", []string{"team"}, onTeam5, 6, []string{"admin"}, false},
		{"no team", []string{"team"}, noTeam, 5, []string{"admin"}, false},
		{"admin bypasses", []string{"admin"}, noTeam, 5, []string{"admin"}, true},
		{"inject bypasses when listed", []string{"inject"}, noTeam, 5, []string{"admin", "inject"}, true},
		{"inject does not bypass when unlisted", []string{"inject"}, noTeam, 5, []string{"admin"}, false},
		{"team zero is not a team", []string{"team"}, noTeam, 0, []string{"admin"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := requireOwnTeam(w, requestAs(tc.roles, tc.identity), tc.teamID, tc.bypassRoles...)
			assert.Equal(t, tc.allowed, got)
			if tc.allowed {
				assert.Equal(t, http.StatusOK, w.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, w.Code)
				assert.JSONEq(t, `{"error":"Forbidden"}`, w.Body.String())
			}
		})
	}
}
