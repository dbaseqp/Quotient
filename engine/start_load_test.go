package engine

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A database error while Start reads the stored rounds must delay scoring, not
// restart it at round 0, whose writes collide with stored rounds.
func TestStart_WaitsForRoundStateAfterLoadError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	for id := uint(1); id <= 3; id++ {
		_, err := pg.DB.CreateRound(db.RoundSchema{ID: id, StartTime: time.Now()})
		require.NoError(t, err)
	}

	se := NewTestEngine(t, redis, pg, 3)
	se.CurrentRound = 0 // a freshly started process

	pg.Exec(t, "ALTER TABLE round_schemas RENAME TO round_schemas_away")
	t.Cleanup(func() { pg.Exec(t, "ALTER TABLE IF EXISTS round_schemas_away RENAME TO round_schemas") })

	firstRound := make(chan uint, 1)
	se.roundFunc = func() error {
		firstRound <- se.CurrentRound
		return errReset
	}
	done := make(chan struct{})
	go func() { defer close(done); se.Start() }()

	time.Sleep(time.Second) // Start is retrying against the missing table
	pg.Exec(t, "ALTER TABLE round_schemas_away RENAME TO round_schemas")

	select {
	case round := <-firstRound:
		assert.Equal(t, uint(4), round, "scoring resumes after the stored rounds")
	case <-time.After(15 * time.Second):
		t.Fatal("no round ran after the database recovered")
	}

	require.NoError(t, se.ResetScores())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after reset")
	}
}

// SLA streaks are replayed in round order, whatever order the rows were stored in.
func TestLoadRoundState_ReplaysStreaksInRoundOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	team, err := pg.DB.CreateTeam(db.TeamSchema{Name: "StartLoadStreakTeam", Active: true, Identifier: "sls1"})
	require.NoError(t, err)

	// Round 2 is stored first, so a scan in storage order sees the fail before the pass.
	for _, round := range []db.RoundSchema{
		{ID: 2, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{{TeamID: team.ID, RoundID: 2, ServiceName: "web", Result: false}}},
		{ID: 1, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{{TeamID: team.ID, RoundID: 1, ServiceName: "web", Result: true}}},
	} {
		_, err := pg.DB.CreateRound(round)
		require.NoError(t, err)
	}

	se := NewTestEngine(t, redis, pg, 3)
	require.NoError(t, se.loadRoundState())
	assert.Equal(t, 1, se.SlaPerService[team.ID]["web"], "round 2 failed after round 1 passed")
}
