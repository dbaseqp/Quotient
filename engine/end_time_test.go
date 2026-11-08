package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseAtCompetitionEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	at := func(d time.Duration) string { return time.Now().UTC().Add(d).Format("2006-01-02 15:04") }
	newEngine := func(t *testing.T, end string) *ScoringEngine {
		t.Helper()
		se := NewTestEngine(t, redis, pg, 3)
		se.Config.InjectSettings = config.InjectConfig{CompetitionEndTime: end, CompetitionTimezone: "UTC"}
		return se
	}
	fresh := func(t *testing.T) {
		pg.ResetCompetitionState(t)
		t.Cleanup(func() { pg.ResetCompetitionState(t) })
	}

	t.Run("no end configured", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, "")
		assert.False(t, se.pauseAtCompetitionEnd())
		assert.False(t, se.Paused())
	})

	t.Run("before the end", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(2*time.Hour))
		assert.False(t, se.pauseAtCompetitionEnd())
		assert.False(t, se.Paused())
	})

	t.Run("after the end pauses once and honors a resume", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		assert.True(t, se.pauseAtCompetitionEnd())
		assert.True(t, se.Paused())

		require.True(t, se.SetPaused(false))
		assert.False(t, se.pauseAtCompetitionEnd(), "an admin resume after the end runs overtime")
		assert.False(t, se.Paused())
	})

	t.Run("a manual pause and resume after the end runs overtime", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, se.SetPaused(true))
		require.True(t, se.SetPaused(false))
		assert.False(t, se.pauseAtCompetitionEnd())
	})

	t.Run("a restart after an overtime resume keeps running", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		first := newEngine(t, end)
		require.True(t, first.pauseAtCompetitionEnd())
		require.True(t, first.SetPaused(false))

		restarted := newEngine(t, end)
		assert.False(t, restarted.pauseAtCompetitionEnd(), "the resume survives a restart before the next round")
	})

	t.Run("a restart without an overtime resume pauses", func(t *testing.T) {
		fresh(t)
		assert.True(t, newEngine(t, at(-2*time.Hour)).pauseAtCompetitionEnd())
	})

	t.Run("a later end pauses again", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, se.pauseAtCompetitionEnd())
		require.True(t, se.SetPaused(false))

		se.Config.InjectSettings.CompetitionEndTime = at(-time.Hour)
		assert.True(t, se.pauseAtCompetitionEnd())
		assert.True(t, se.Paused())
	})
}

func TestResetScoresForgetsOvertime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })

	end := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	require.NoError(t, pg.DB.SetOvertimeEnd(end))
	got, err := pg.DB.GetOvertimeEnd()
	require.NoError(t, err)
	require.True(t, end.Equal(got))

	require.NoError(t, pg.DB.ResetScores())
	got, err = pg.DB.GetOvertimeEnd()
	require.NoError(t, err)
	assert.True(t, got.IsZero(), "an overtime resume from before a reset must not outlive it")
	assert.True(t, pg.DB.GetCompetitionStarted(), "a reset leaves the competition started")
}

func TestCompetitionState_ConcurrentStartAndOvertime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })

	end := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 10 {
			require.NoError(t, pg.DB.SetCompetitionStarted(true))
		}
	}()
	go func() {
		defer wg.Done()
		for range 10 {
			require.NoError(t, pg.DB.SetOvertimeEnd(end))
		}
	}()
	wg.Wait()

	assert.True(t, pg.DB.GetCompetitionStarted())
	gotEnd, err := pg.DB.GetOvertimeEnd()
	require.NoError(t, err)
	assert.True(t, end.Equal(gotEnd))
}
