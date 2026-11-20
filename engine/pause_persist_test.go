package engine

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootEngine builds an engine the way NewEngine does after a process restart.
func bootEngine(t *testing.T, redis *testutil.RedisContainer, pg *testutil.PostgresContainer, startPaused bool) *ScoringEngine {
	t.Helper()
	se := NewTestEngine(t, redis, pg, 3)
	se.Config.Current().MiscSettings.StartPaused = startPaused
	require.NoError(t, se.loadPauseState())
	return se
}

// With StartPaused and a scheduled start, a restart after the start keeps
// scoring.
func TestLoadPauseState_RestartAfterScheduledStartKeepsScoring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)

	first := bootEngine(t, redis, pg, true)
	require.True(t, first.Paused())
	require.True(t, first.startScheduledCompetition(time.Now().Add(-time.Hour)))
	require.False(t, first.Paused())

	assert.False(t, bootEngine(t, redis, pg, true).Paused(), "the scheduled start's resume survives a restart")
}

// An admin pause or resume survives a restart and a score reset.
func TestSetPaused_SurvivesRestartAndReset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)
	se := bootEngine(t, redis, pg, false)

	require.NoError(t, se.SetPaused(true))
	assert.True(t, bootEngine(t, redis, pg, false).Paused(), "an admin pause survives a restart")

	require.NoError(t, se.SetPaused(false))
	assert.False(t, bootEngine(t, redis, pg, true).Paused(), "an admin resume survives a restart")

	require.NoError(t, se.ResetScores())
	assert.False(t, se.Paused(), "a reset keeps the running engine's state")
	assert.False(t, bootEngine(t, redis, pg, true).Paused(), "a restart after a reset keeps the running engine's state")
}

// A pause whose write fails leaves the engine running, so the running engine
// never disagrees with what a restart restores, and a retry is not rejected.
func TestSetPaused_FailedWriteLeavesEngineUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	se := bootEngine(t, redis, pg, false)

	dropTrigger := func() { pg.Exec(t, "DROP TRIGGER IF EXISTS reject_write ON competition_state_schemas") }
	dropFunction := func() { pg.Exec(t, "DROP FUNCTION IF EXISTS reject_competition_state_write()") }
	t.Cleanup(func() {
		dropTrigger()
		dropFunction()
	})
	dropTrigger() // a killed earlier run may have left these behind
	dropFunction()
	pg.Exec(t, `CREATE FUNCTION reject_competition_state_write() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'competition_state write rejected by test'; END $$`)
	pg.Exec(t, "CREATE TRIGGER reject_write BEFORE UPDATE ON competition_state_schemas EXECUTE FUNCTION reject_competition_state_write()")

	require.Error(t, se.SetPaused(true))
	assert.False(t, se.Paused(), "a pause that was not saved does not take effect")

	dropTrigger()
	require.NoError(t, se.SetPaused(true), "the retry is a real transition")
	assert.True(t, se.Paused())
}
