package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseAtCompetitionEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	at := func(d time.Duration) *time.Time {
		end := time.Now().Add(d).Truncate(time.Second)
		return &end
	}
	newEngine := func(t *testing.T, end *time.Time) *ScoringEngine {
		t.Helper()
		se := NewTestEngine(t, redis, pg, 3)
		se.Config.InjectSettings.End = end
		return se
	}
	fresh := func(t *testing.T) {
		pg.ResetCompetitionState(t)
		t.Cleanup(func() { pg.ResetCompetitionState(t) })
	}
	overtime := func(t *testing.T) time.Time {
		t.Helper()
		got, err := pg.DB.GetOvertimeEnd()
		require.NoError(t, err)
		return got
	}

	t.Run("no end configured", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, nil)
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
		end := at(-2 * time.Hour)
		se := newEngine(t, end)
		assert.True(t, se.pauseAtCompetitionEnd())
		assert.True(t, se.Paused())
		assert.True(t, overtime(t).IsZero(), "pausing at the end is not overtime yet")

		require.True(t, se.SetPaused(false))
		assert.True(t, end.Equal(overtime(t)), "the resume is persisted as overtime")
		assert.False(t, se.pauseAtCompetitionEnd(), "an admin resume after the end runs overtime")
		assert.False(t, se.Paused())
	})

	t.Run("the resume is persisted before the loop is released", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		se := newEngine(t, end)
		require.True(t, se.pauseAtCompetitionEnd())

		// Hold the state row so the overtime write cannot commit: a resume that
		// released the loop first would let it read a NULL overtime and pause again.
		release := pg.Hold(t, "SELECT id FROM competition_state_schemas WHERE id = 1 FOR UPDATE")
		resumed := make(chan bool, 1)
		go func() { resumed <- se.SetPaused(false) }()
		select {
		case <-resumed:
			t.Fatal("resume returned without persisting overtime")
		case <-time.After(300 * time.Millisecond):
		}
		assert.True(t, se.Paused(), "the loop stays blocked until overtime is committed")

		release()
		assert.True(t, <-resumed)
		assert.True(t, end.Equal(overtime(t)))
		assert.False(t, se.pauseAtCompetitionEnd(), "the woken loop runs overtime")
		assert.False(t, se.Paused())
	})

	t.Run("a manual pause before the end is not overtime after it", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(time.Hour))
		require.True(t, se.SetPaused(true))

		se.Config.InjectSettings.End = at(-time.Hour) // the end passes while paused
		require.True(t, se.SetPaused(false))
		assert.True(t, overtime(t).IsZero(), "a resume from a manual pause records no overtime")
		assert.True(t, se.pauseAtCompetitionEnd(), "the end is enforced at the next round boundary")
		assert.True(t, se.Paused())

		require.True(t, se.SetPaused(false))
		assert.False(t, se.pauseAtCompetitionEnd(), "resuming after the engine paused at the end runs overtime")
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

		se.Config.InjectSettings.End = at(-time.Hour)
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
	require.NoError(t, pg.DB.SetOvertimeEnd(&end))
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
			require.NoError(t, pg.DB.SetOvertimeEnd(&end))
		}
	}()
	wg.Wait()

	assert.True(t, pg.DB.GetCompetitionStarted())
	gotEnd, err := pg.DB.GetOvertimeEnd()
	require.NoError(t, err)
	assert.True(t, end.Equal(gotEnd))
}

func TestCompetitionState_LegacyRowIsNormalized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })

	// A pre-normalization database: only a row with an auto-assigned id.
	startedAt := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	pg.Exec(t, "DELETE FROM competition_state_schemas")
	pg.Exec(t, "INSERT INTO competition_state_schemas (id, started, started_at) VALUES (5, true, ?)", startedAt)

	d := pg.Reconnect(t) // runs migrate: normalization, then the seed, which must not add a second row
	assert.True(t, d.GetCompetitionStarted(), "the legacy row is read as the singleton")
	gotStart, err := d.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, gotStart)
	assert.True(t, startedAt.Equal(*gotStart))

	end := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	require.NoError(t, d.SetOvertimeEnd(&end))
	require.NoError(t, d.SetCompetitionStarted(false))
	assert.False(t, d.GetCompetitionStarted())
	gotEnd, err := d.GetOvertimeEnd()
	require.NoError(t, err)
	assert.True(t, end.Equal(gotEnd), "writes land on the same row as reads")
	gotStart, err = d.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, gotStart)
	assert.True(t, startedAt.Equal(*gotStart), "the first start survives normalization and later writes")
}

func TestCompetitionState_SeededOnMigrate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })
	pg.Exec(t, "DELETE FROM competition_state_schemas")

	d := pg.Reconnect(t)
	got, err := d.GetOvertimeEnd()
	require.NoError(t, err, "migrate seeds the singleton row")
	assert.True(t, got.IsZero())
	require.NoError(t, d.SetCompetitionStarted(true))
	assert.True(t, d.GetCompetitionStarted())
}
