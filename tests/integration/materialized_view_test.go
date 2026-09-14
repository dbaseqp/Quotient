package integration

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/require"
)

func TestMaterializedViewLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Start PostgreSQL connection
	// This calls createCumulativeScoresView() internally, which now includes the initial REFRESH
	_, pg := testutil.StartContainers(t)

	// Data cleanup
	err := pg.DB.ResetScores()
	require.NoError(t, err, "ResetScores should succeed")

	t.Run("refresh with zero rows", func(t *testing.T) {
		// The view should operate correctly even with no data
		err := pg.DB.RefreshScoresMaterializedView()
		require.NoError(t, err, "RefreshScoresMaterializedView should succeed with 0 rows")
	})

	t.Run("refresh with data", func(t *testing.T) {
		// Add a team
		team := db.TeamSchema{
			Name:       "ViewTestTeam",
			Active:     true,
			Identifier: "vt1",
		}
		teamCreated, err := pg.DB.CreateTeam(team)
		require.NoError(t, err)

		// Create a round with a result
		check := db.ServiceCheckSchema{
			TeamID:      teamCreated.ID,
			RoundID:     1,
			ServiceName: "test-service",
			Points:      10,
			Result:      true,
		}

		round := db.RoundSchema{
			ID:        1,
			StartTime: time.Now(),
			Checks:    []db.ServiceCheckSchema{check},
		}

		_, err = pg.DB.CreateRound(round)
		require.NoError(t, err, "should save round to database")

		// Refresh should succeed with data
		err = pg.DB.RefreshScoresMaterializedView()
		require.NoError(t, err, "RefreshScoresMaterializedView should succeed with data")

		// Optional: We could verify data via db.GetServiceCheckSumByRound() if we wanted to be thorough
		// but the main point here is that the REFRESH command doesn't throw an error.
	})
}
