package integration

import (
	"quotient/engine/db"
	"quotient/tests/testutil"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMaterializedViewLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Start PostgreSQL connection
	pgContainer := testutil.StartPostgres(t)

	// Initialize database connection
	// This calls createCumulativeScoresView() internally, which now includes the initial REFRESH
	db.Connect(pgContainer.ConnectionString())

	// Data cleanup
	err := db.ResetScores()
	require.NoError(t, err, "ResetScores should succeed")

	t.Run("refresh with zero rows", func(t *testing.T) {
		// The view should operate correctly even with no data
		err := db.RefreshScoresMaterializedView()
		require.NoError(t, err, "RefreshScoresMaterializedView should succeed with 0 rows")
	})

	t.Run("refresh with data", func(t *testing.T) {
		// Add a team
		team := db.TeamSchema{
			Name:       "ViewTestTeam",
			Active:     true,
			Identifier: "vt1",
		}
		teamCreated, err := db.CreateTeam(team)
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

		_, err = db.CreateRound(round)
		require.NoError(t, err, "should save round to database")

		// Refresh should succeed with data
		err = db.RefreshScoresMaterializedView()
		require.NoError(t, err, "RefreshScoresMaterializedView should succeed with data")

		// Optional: We could verify data via db.GetServiceCheckSumByRound() if we wanted to be thorough
		// but the main point here is that the REFRESH command doesn't throw an error.
	})
}
