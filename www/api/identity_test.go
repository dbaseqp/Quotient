package api

import (
	"context"
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
		expected string
		ok       bool
	}{
		{"quotient-blue-Team-05", "5", true},
		{"quotient-blue-Team-5", "5", true},
		{"team05", "5", true},
		{"team5", "5", true},
		{"Team 5", "5", true},
		{"WCComps_Quotient_Blue_Team01", "1", true},
		{"quotient-blue-Team-05b", "5b", true},
		{"quotient-blue-Team-05B", "5b", true},
		{"team10", "10", true},
		{"team100", "100", true},
		{"redteam", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := teamOrdinal(tc.name)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// The reported failure: a user named "hola" in group "quotient-blue-Team-05"
// submitting a PCR for team05.
func TestMapOIDCUserToTeamReportedCase(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team02", "team03", "team04", "team05")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"})
	require.NotNil(t, team)
	assert.Equal(t, "team05", team.Name)
	assert.Equal(t, uint(5), team.ID)
}

func TestMapOIDCUserToTeamUnpaddedTeamNames(t *testing.T) {
	// The shipped event.conf.example names teams "team1", "team2" - unpadded.
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

func TestMapOIDCUserToTeamDivisionSuffix(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team05a", "team05b", "team05c")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05b"})
	require.NotNil(t, team)
	assert.Equal(t, "team05b", team.Name)

	// A group with no division must not silently land on a division team.
	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"}))
}

func TestMapOIDCUserToTeamExactNameMatch(t *testing.T) {
	withOIDCConfig(t, []string{"BlueAlpha", "BlueBravo"}, nil)
	teams := teamList("BlueAlpha", "BlueBravo")

	team := mapOIDCUserToTeam(teams, []string{"bluebravo"})
	require.NotNil(t, team)
	assert.Equal(t, "BlueBravo", team.Name)
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
	// Group names carrying no team ordinal at all are only resolvable via the
	// explicit map.
	withOIDCConfig(t, []string{"ccdc-blue-*"}, map[string]string{
		"ccdc-blue-charlie": "team03",
	})
	teams := teamList("team01", "team02", "team03")

	team := mapOIDCUserToTeam(teams, []string{"ccdc-blue-charlie"})
	require.NotNil(t, team)
	assert.Equal(t, "team03", team.Name)
}

func TestMapOIDCUserToTeamRefusesAmbiguousMatch(t *testing.T) {
	// "team5" and "team05" both reduce to ordinal 5. Guessing would put the
	// user on the wrong team, so nothing is resolved.
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team5", "team05")

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-05"}))
}

func TestMapOIDCUserToTeamIgnoresNonTeamGroups(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team05")

	// A group outside the configured team patterns must not assign a team,
	// even though it ends in digits.
	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"vpn-users-05", "some-other-group"}))
}

func TestMapOIDCUserToTeamNoMatch(t *testing.T) {
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team01", "team02")

	assert.Nil(t, mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-99"}))
	assert.Nil(t, mapOIDCUserToTeam(teams, nil))
}

func TestMapOIDCUserToTeamNegativeLookingSuffix(t *testing.T) {
	// The previous implementation read the last two characters and called
	// strconv.Atoi, so "-5" parsed as -5 and produced the team name "team-5".
	withOIDCConfig(t, []string{"quotient-blue-*"}, nil)
	teams := teamList("team05")

	team := mapOIDCUserToTeam(teams, []string{"quotient-blue-Team-5"})
	require.NotNil(t, team)
	assert.Equal(t, "team05", team.Name)
}

// Local and LDAP accounts are named after their team, so the rule for both is
// a plain name match.
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

// A team ID must never be readable without the caller learning whether one
// exists. Reading team zero as a real team is what produced the original 403.
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

// The identity carried on the context is the one the middleware stored, not a
// value any handler re-derived.
func TestIdentityRoundTripsThroughContext(t *testing.T) {
	want := Identity{Username: "hola", AuthSource: "oidc", Roles: []string{"team"}, TeamID: 5, HasTeam: true}
	got, ok := IdentityFrom(WithIdentity(context.Background(), want))
	require.True(t, ok)
	assert.Equal(t, want, got)

	_, ok = IdentityFrom(context.Background())
	assert.False(t, ok)
}
