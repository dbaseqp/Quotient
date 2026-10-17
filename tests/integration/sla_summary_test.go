package integration

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamSummarySumsWaivedSLAPenalties(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team Waived", "01")
	serviceName := "waived-service"

	for i := uint(1); i <= 2; i++ {
		_, err := pg.DB.CreateRound(db.RoundSchema{
			ID:        i,
			StartTime: time.Now(),
			Checks: []db.ServiceCheckSchema{{
				TeamID:      team.ID,
				RoundID:     i,
				ServiceName: serviceName,
				Result:      false,
			}},
		})
		require.NoError(t, err)
	}

	_, err := pg.DB.CreateSLA(db.SLASchema{TeamID: team.ID, RoundID: 1, ServiceName: serviceName, Penalty: 0, WaivedPenalty: 7})
	require.NoError(t, err)
	_, err = pg.DB.CreateSLA(db.SLASchema{TeamID: team.ID, RoundID: 2, ServiceName: serviceName, Penalty: 5})
	require.NoError(t, err)

	summaries, err := pg.DB.GetTeamSummary(team.ID)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, 2, summaries[0]["SlaCount"])
	assert.Equal(t, 7, summaries[0]["SlaWaivedPoints"])
}
