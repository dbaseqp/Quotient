package integration

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/require"
)

func TestGetCheckResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())

	team, err := pg.DB.CreateTeam(db.TeamSchema{Name: "CheckResultsTeam", Active: true, Identifier: "cr1"})
	require.NoError(t, err)
	other, err := pg.DB.CreateTeam(db.TeamSchema{Name: "CheckResultsOther", Active: true, Identifier: "cr2"})
	require.NoError(t, err)

	for _, round := range []db.RoundSchema{
		{ID: 2, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{
			{TeamID: team.ID, RoundID: 2, ServiceName: "web", Result: false, Error: "connection refused", Debug: "dialing"},
			{TeamID: other.ID, RoundID: 2, ServiceName: "ftp", Result: true},
		}},
		{ID: 1, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{
			{TeamID: team.ID, RoundID: 1, ServiceName: "web", Result: true, Points: 5},
			{TeamID: team.ID, RoundID: 1, ServiceName: "dns", Result: true, Points: 5},
		}},
	} {
		_, err := pg.DB.CreateRound(round)
		require.NoError(t, err)
	}

	results, err := pg.DB.GetCheckResults([]uint{team.ID, other.ID})
	require.NoError(t, err)
	require.Len(t, results, 4)
	for i := 1; i < len(results); i++ {
		require.LessOrEqual(t, results[i-1].RoundID, results[i].RoundID, "results arrive in round order")
	}
	require.Contains(t, results, db.CheckResult{RoundID: 2, TeamID: team.ID, ServiceName: "web", Result: false})
	require.Contains(t, results, db.CheckResult{RoundID: 1, TeamID: team.ID, ServiceName: "dns", Result: true})

	results, err = pg.DB.GetCheckResults([]uint{other.ID})
	require.NoError(t, err)
	require.Equal(t, []db.CheckResult{{RoundID: 2, TeamID: other.ID, ServiceName: "ftp", Result: true}}, results,
		"only the given teams' results")

	results, err = pg.DB.GetCheckResults(nil)
	require.NoError(t, err)
	require.Empty(t, results)
}
