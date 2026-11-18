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
	se.Config.MiscSettings.StartPaused = startPaused
	require.NoError(t, se.loadPauseState())
	return se
}

// With StartPaused and a scheduled start, a restart after the start must keep
// scoring instead of coming back paused until an admin notices.
func TestLoadPauseState_RestartAfterScheduledStartKeepsScoring(t *testing.T) {
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)

	first := bootEngine(t, redis, pg, true)
	require.True(t, first.Paused())
	require.True(t, first.startScheduledCompetition(time.Now().Add(-time.Hour)))
	require.False(t, first.Paused())

	assert.False(t, bootEngine(t, redis, pg, true).Paused(), "the scheduled start's resume survives a restart")
}

// An admin pause (e.g. during an infrastructure outage) must survive a
// restart, and a score reset forgets it so StartPaused applies again.
func TestLoadPauseState_AdminPauseSurvivesRestartUntilReset(t *testing.T) {
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)

	require.NoError(t, pg.DB.SetPaused(true))
	assert.True(t, bootEngine(t, redis, pg, false).Paused(), "an admin pause survives a restart")

	require.NoError(t, pg.DB.SetPaused(false))
	assert.False(t, bootEngine(t, redis, pg, true).Paused(), "an admin resume survives a restart")

	require.NoError(t, pg.DB.ResetScores())
	assert.True(t, bootEngine(t, redis, pg, true).Paused(), "after a reset StartPaused applies again")
}
