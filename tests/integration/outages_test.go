package integration

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamOutages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())

	recovered := pg.CreateTestTeam(t, "Outage Recovered", "01")
	dark := pg.CreateTestTeam(t, "Outage Dark", "02")
	alwaysDark := pg.CreateTestTeam(t, "Outage Always", "03")
	partial := pg.CreateTestTeam(t, "Outage Partial", "04")
	gone := pg.CreateTestTeam(t, "Outage Gone", "05")

	check := func(team db.TeamSchema, round uint, service string, result bool) db.ServiceCheckSchema {
		return db.ServiceCheckSchema{TeamID: team.ID, RoundID: round, ServiceName: service, Result: result}
	}
	start := time.Date(2026, 10, 31, 19, 0, 0, 0, time.UTC)
	rounds := map[uint][]db.ServiceCheckSchema{
		1: {check(recovered, 1, "web", false), check(dark, 1, "web", true), check(alwaysDark, 1, "web", false), check(partial, 1, "web", true), check(gone, 1, "web", false)},
		2: {check(recovered, 2, "web", false), check(dark, 2, "web", false), check(dark, 2, "dns", false), check(alwaysDark, 2, "web", false), check(partial, 2, "web", false), check(partial, 2, "dns", true)},
		// round 3 was discarded (no results); round IDs still advance
		4: {check(recovered, 4, "web", true), check(dark, 4, "web", false), check(dark, 4, "dns", false), check(alwaysDark, 4, "web", false), check(partial, 4, "web", false), check(partial, 4, "dns", true)},
	}
	for id, checks := range rounds {
		_, err := pg.DB.CreateRound(db.RoundSchema{ID: id, StartTime: start.Add(time.Duration(id) * 2 * time.Minute), Checks: checks})
		require.NoError(t, err)
	}

	outages, err := pg.DB.GetTeamOutages()
	require.NoError(t, err)

	byTeam := map[string]db.TeamOutage{}
	for _, o := range outages {
		byTeam[o.TeamName] = o
	}
	require.Len(t, byTeam, 2, "only teams with every check failing in the latest round: %+v", outages)

	assert.Equal(t, 2, byTeam[dark.Name].RoundsDown, "counts rounds the team was scored in, not round IDs")
	assert.True(t, start.Add(4*time.Minute).Equal(byTeam[dark.Name].DownSince))

	assert.Equal(t, 3, byTeam[alwaysDark.Name].RoundsDown, "a team that never passed is down since its first round")
	assert.True(t, start.Add(2*time.Minute).Equal(byTeam[alwaysDark.Name].DownSince))
}
