package integration

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/require"
)

func TestGetServiceScoresReportsEarnedAndPossiblePoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "ServiceScoresTeam", "ss1")
	late := pg.CreateTestTeam(t, "ServiceScoresLate", "ss2")

	for _, round := range []db.RoundSchema{
		{ID: 1, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{
			{TeamID: team.ID, RoundID: 1, ServiceName: "web", Result: true, Points: 5},
			{TeamID: team.ID, RoundID: 1, ServiceName: "dns", Result: false, Points: 3},
		}},
		{ID: 2, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{
			{TeamID: team.ID, RoundID: 2, ServiceName: "web", Result: false, Points: 5},
			{TeamID: team.ID, RoundID: 2, ServiceName: "dns", Result: false, Points: 3},
			{TeamID: late.ID, RoundID: 2, ServiceName: "web", Result: true, Points: 5},
		}},
	} {
		_, err := pg.DB.CreateRound(round)
		require.NoError(t, err)
	}

	scores, err := pg.DB.GetServiceScores()
	require.NoError(t, err)
	require.Equal(t, []db.ServiceScoreData{
		{TeamID: team.ID, ServiceName: "dns", Points: 0, MaxPoints: 6},
		{TeamID: team.ID, ServiceName: "web", Points: 5, MaxPoints: 10},
		{TeamID: late.ID, ServiceName: "web", Points: 5, MaxPoints: 5},
	}, scores, "possible points count failed checks; a team checked in fewer rounds has a lower maximum")
}
