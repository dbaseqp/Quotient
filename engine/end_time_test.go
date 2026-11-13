package engine

import (
	"sync"
	"sync/atomic"
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
	overtime := func(t *testing.T) *time.Time {
		t.Helper()
		state, err := pg.DB.GetCompetitionState()
		require.NoError(t, err)
		return state.OvertimeEnd
	}
	// boundary is what the round loop does before a round: read the schedule
	// once and pause if the end has passed.
	boundary := func(t *testing.T, se *ScoringEngine) bool {
		t.Helper()
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		return se.pauseAtCompetitionEnd(s)
	}
	// pausedAtEnd is loop-owned; tests read it only between direct boundary calls.
	pausedAtEnd := func(se *ScoringEngine) *time.Time { return se.pausedAtEnd }

	t.Run("no end configured", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, nil)
		assert.False(t, boundary(t, se))
		assert.False(t, se.Paused())
	})

	t.Run("before the end", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(2*time.Hour))
		assert.False(t, boundary(t, se))
		assert.False(t, se.Paused())
	})

	t.Run("after the end pauses once and a resume is persisted at the next boundary", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		se := newEngine(t, end)
		assert.True(t, boundary(t, se))
		assert.True(t, se.Paused())
		assert.Nil(t, overtime(t), "pausing at the end is not overtime yet")

		require.True(t, se.SetPaused(false))
		assert.Nil(t, overtime(t), "the resume itself writes nothing; the loop does")
		assert.False(t, boundary(t, se), "an admin resume after the end runs overtime")
		require.NotNil(t, overtime(t), "the loop persists the resume as overtime")
		assert.True(t, end.Equal(*overtime(t)))
		assert.Nil(t, pausedAtEnd(se))
		assert.False(t, se.Paused())
	})

	t.Run("a failed overtime write is retried at the next boundary", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		se := newEngine(t, end)
		require.True(t, boundary(t, se))
		require.True(t, se.SetPaused(false))

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

		assert.False(t, boundary(t, se), "a failed overtime write does not pause the loop again")
		assert.Nil(t, overtime(t))
		require.NotNil(t, pausedAtEnd(se), "the pending end-pause is kept for a retry")
		assert.True(t, end.Equal(*pausedAtEnd(se)))

		dropTrigger()
		assert.False(t, boundary(t, se))
		require.NotNil(t, overtime(t), "the retry persists overtime")
		assert.True(t, end.Equal(*overtime(t)))
		assert.Nil(t, pausedAtEnd(se))
	})

	t.Run("a manual pause before the end is not overtime after it", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(time.Hour))
		require.True(t, se.SetPaused(true))

		se.Config.InjectSettings.End = at(-time.Hour) // the end passes while paused
		require.True(t, se.SetPaused(false))
		assert.True(t, boundary(t, se), "the end is enforced at the next round boundary")
		assert.True(t, se.Paused())
		assert.Nil(t, overtime(t), "a resume from a manual pause records no overtime")

		require.True(t, se.SetPaused(false))
		assert.False(t, boundary(t, se), "resuming after the engine paused at the end runs overtime")
		require.NotNil(t, overtime(t))
		assert.True(t, se.Config.InjectSettings.End.Equal(*overtime(t)))
	})

	t.Run("an admin pause landing before the end check re-blocks the loop", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, se.SetPaused(true))
		assert.True(t, boundary(t, se), "the loop waits on the admin's pause instead of running a round")
		assert.Nil(t, pausedAtEnd(se), "the admin's pause is not an end-pause")
		assert.Nil(t, overtime(t))
	})

	t.Run("an end moved into the future forgets a pending end-pause", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, boundary(t, se))
		require.True(t, se.SetPaused(false))

		se.Config.InjectSettings.End = at(2 * time.Hour)
		assert.False(t, boundary(t, se))
		assert.Nil(t, pausedAtEnd(se))
		assert.Nil(t, overtime(t), "a resume after a superseded end is not overtime")
	})

	t.Run("a pending end-pause for a superseded end is forgotten even when the new end is overtime", func(t *testing.T) {
		fresh(t)
		old := at(-2 * time.Hour)
		se := newEngine(t, old)
		require.True(t, boundary(t, se))
		require.True(t, se.SetPaused(false))

		later := at(-time.Hour)
		require.NoError(t, pg.DB.SetOvertimeEnd(later), "an earlier run already resumed past the new end")
		se.Config.InjectSettings.End = later
		assert.False(t, boundary(t, se))
		assert.Nil(t, pausedAtEnd(se))

		se.Config.InjectSettings.End = old
		assert.True(t, boundary(t, se), "reverting to the old end pauses instead of granting its overtime")
		require.NotNil(t, overtime(t))
		assert.True(t, later.Equal(*overtime(t)))
	})

	t.Run("Start forgets a pending end-pause taken before it ran", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		se := newEngine(t, end)
		require.True(t, boundary(t, se))

		done := make(chan struct{})
		go func() {
			defer close(done)
			se.Start()
		}()
		require.Eventually(t, func() bool {
			return redis.Client.PubSubNumSub(t.Context(), "events").Val()["events"] >= 1
		}, 2*time.Second, 20*time.Millisecond)
		require.NoError(t, se.ResetScores())
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Start did not return after ResetScores")
		}

		require.True(t, se.SetPaused(false))
		assert.True(t, boundary(t, se), "the resume after a reset is not overtime for the old end")
		assert.Nil(t, overtime(t))
	})

	t.Run("a restart after an overtime resume keeps running", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		first := newEngine(t, end)
		require.True(t, boundary(t, first))
		require.True(t, first.SetPaused(false))
		require.False(t, boundary(t, first), "the loop persists overtime at its next boundary")

		restarted := newEngine(t, end)
		assert.False(t, boundary(t, restarted), "the resume survives a restart")
	})

	t.Run("a crash before the loop persists the resume pauses again on restart", func(t *testing.T) {
		fresh(t)
		end := at(-2 * time.Hour)
		first := newEngine(t, end)
		require.True(t, boundary(t, first))
		require.True(t, first.SetPaused(false))

		restarted := newEngine(t, end)
		assert.True(t, boundary(t, restarted), "an unpersisted resume does not survive a restart")
	})

	t.Run("a restart without an overtime resume pauses", func(t *testing.T) {
		fresh(t)
		assert.True(t, boundary(t, newEngine(t, at(-2*time.Hour))))
	})

	t.Run("a later end pauses again", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, boundary(t, se))
		require.True(t, se.SetPaused(false))
		require.False(t, boundary(t, se))

		se.Config.InjectSettings.End = at(-time.Hour)
		assert.True(t, boundary(t, se))
		assert.True(t, se.Paused())
	})

	t.Run("a runtime end replaces the configured end", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(2*time.Hour))
		require.NoError(t, se.SetCompetitionSchedule(nil, at(-time.Hour)))
		assert.True(t, boundary(t, se))
		assert.True(t, se.Paused())

		require.True(t, se.SetPaused(false))
		assert.False(t, boundary(t, se), "overtime is granted for the runtime end")
		require.NoError(t, se.SetCompetitionSchedule(nil, nil))
		assert.False(t, boundary(t, se), "clearing falls back to the configured end, which is ahead")
	})

	t.Run("a runtime end written while paused at the old end supersedes it", func(t *testing.T) {
		fresh(t)
		oldEnd := at(-2 * time.Hour)
		se := newEngine(t, oldEnd)
		require.True(t, boundary(t, se))

		newEnd := at(-time.Hour)
		require.NoError(t, se.SetCompetitionSchedule(nil, newEnd))
		require.NotNil(t, pausedAtEnd(se), "the write does not touch pausedAtEnd; the next boundary decides")
		assert.True(t, oldEnd.Equal(*pausedAtEnd(se)))

		require.True(t, se.SetPaused(false))
		assert.True(t, boundary(t, se), "the old end no longer equals the effective end, so the new end is enforced")
		assert.Nil(t, overtime(t), "a resume after a superseded end is not overtime for the new end")
		require.NotNil(t, pausedAtEnd(se))
		assert.True(t, newEnd.Equal(*pausedAtEnd(se)))
	})

	t.Run("a configured end changed while paused at the old end is not promoted", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, at(-2*time.Hour))
		require.True(t, boundary(t, se))

		se.Config.InjectSettings.End = at(-time.Hour) // an event.conf hot-reload
		require.True(t, se.SetPaused(false))
		assert.Nil(t, overtime(t), "overtime is recorded only for the end that is still in effect")
		assert.True(t, boundary(t, se))
	})

	t.Run("restoring a previous runtime end does not re-arm its overtime", func(t *testing.T) {
		fresh(t)
		se := newEngine(t, nil)
		end := at(-time.Hour)
		require.NoError(t, se.SetCompetitionSchedule(nil, end))
		require.True(t, boundary(t, se))
		require.True(t, se.SetPaused(false))
		require.False(t, boundary(t, se), "overtime is granted for the runtime end")
		require.NotNil(t, overtime(t))

		require.NoError(t, se.SetCompetitionSchedule(nil, at(time.Hour)))
		assert.Nil(t, overtime(t), "a changed end forgets overtime")
		assert.False(t, boundary(t, se))

		require.NoError(t, se.SetCompetitionSchedule(nil, end))
		assert.True(t, boundary(t, se), "the restored end pauses again until an admin resumes past it")
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
	state, err := pg.DB.GetCompetitionState()
	require.NoError(t, err)
	require.NotNil(t, state.OvertimeEnd)
	require.True(t, end.Equal(*state.OvertimeEnd))

	require.NoError(t, pg.DB.ResetScores())
	state, err = pg.DB.GetCompetitionState()
	require.NoError(t, err)
	assert.Nil(t, state.OvertimeEnd, "an overtime resume from before a reset must not outlive it")
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
	state, err := pg.DB.GetCompetitionState()
	require.NoError(t, err)
	require.NotNil(t, state.OvertimeEnd)
	assert.True(t, end.Equal(*state.OvertimeEnd))
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
	state, err := d.GetCompetitionState()
	require.NoError(t, err)
	require.NotNil(t, state.OvertimeEnd)
	assert.True(t, end.Equal(*state.OvertimeEnd), "writes land on the same row as reads")
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
	state, err := d.GetCompetitionState()
	require.NoError(t, err, "migrate seeds the singleton row")
	assert.Nil(t, state.OvertimeEnd)
	require.NoError(t, d.SetCompetitionStarted(true))
	assert.True(t, d.GetCompetitionStarted())
}

func TestCompetitionState_ConcurrentStartOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })

	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	var started atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := pg.DB.StartCompetitionOnce(start)
			require.NoError(t, err)
			if ok {
				started.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), started.Load(), "exactly one caller starts the competition on an empty table")
	gotStart, err := pg.DB.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, gotStart)
	assert.True(t, start.Equal(*gotStart))
}

func TestCompetitionSchedule_OverrideAndAutoStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	fresh := func(t *testing.T) {
		pg.ResetCompetitionState(t)
		t.Cleanup(func() { pg.ResetCompetitionState(t) })
	}
	at := func(d time.Duration) *time.Time {
		instant := time.Now().Add(d).Truncate(time.Millisecond)
		return &instant
	}
	// waitInBackground runs waitForRoundReady until the test ends.
	waitInBackground := func(t *testing.T, se *ScoringEngine) <-chan bool {
		t.Helper()
		stop := make(chan struct{})
		t.Cleanup(func() { close(stop) })
		ready := make(chan bool, 1)
		go func() {
			_, ok := se.waitForRoundReady(stop)
			ready <- ok
		}()
		return ready
	}
	expectReady := func(t *testing.T, ready <-chan bool, within time.Duration) {
		t.Helper()
		select {
		case ok := <-ready:
			require.True(t, ok)
		case <-time.After(within):
			t.Fatal("waitForRoundReady did not return")
		}
	}
	expectBlocked := func(t *testing.T, ready <-chan bool, d time.Duration) {
		t.Helper()
		select {
		case ok := <-ready:
			t.Fatalf("waitForRoundReady returned %v", ok)
		case <-time.After(d):
		}
	}
	pendingStart := func(t *testing.T, se *ScoringEngine) bool {
		t.Helper()
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		_, pending := s.nextScheduledStart()
		return pending
	}
	overtime := func(t *testing.T) *time.Time {
		t.Helper()
		state, err := pg.DB.GetCompetitionState()
		require.NoError(t, err)
		return state.OvertimeEnd
	}

	t.Run("a runtime schedule replaces event.conf and clearing restores it", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		confStart := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
		confEnd := time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)
		se.Config.InjectSettings = config.InjectConfig{Start: &confStart, End: &confEnd}

		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		assert.Equal(t, Schedule{Start: &confStart, End: &confEnd, Source: "config"}, s)

		newStart := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
		require.NoError(t, se.SetCompetitionSchedule(&newStart, nil))

		restarted := NewTestEngine(t, redis, pg, 3)
		restarted.Config.InjectSettings = se.Config.InjectSettings
		s, err = restarted.CompetitionSchedule()
		require.NoError(t, err)
		assert.Equal(t, "runtime", s.Source)
		require.NotNil(t, s.Start)
		assert.True(t, s.Start.Equal(newStart))
		assert.Nil(t, s.End, "the runtime schedule replaces event.conf as a whole")

		require.NoError(t, restarted.SetCompetitionSchedule(nil, nil))
		s, err = restarted.CompetitionSchedule()
		require.NoError(t, err)
		assert.Equal(t, Schedule{Start: &confStart, End: &confEnd, Source: "config"}, s)

		restarted.Config.InjectSettings = config.InjectConfig{}
		s, err = restarted.CompetitionSchedule()
		require.NoError(t, err)
		assert.Equal(t, Schedule{Source: "none"}, s)
	})

	t.Run("end not after start is rejected", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		start := at(time.Hour)
		assert.ErrorIs(t, se.SetCompetitionSchedule(start, start), ErrEndBeforeStart)
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		assert.Equal(t, "none", s.Source)
	})

	t.Run("the round loop performs a due start while paused", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		require.True(t, se.SetPaused(true))
		pastStart := at(-time.Minute)
		require.NoError(t, se.SetCompetitionSchedule(pastStart, at(time.Hour)))
		assert.False(t, pg.DB.GetCompetitionStarted(), "writing the schedule does not start the competition")

		expectReady(t, waitInBackground(t, se), 2*time.Second)
		assert.False(t, se.Paused())
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		assert.True(t, s.Started)
		require.NotNil(t, s.StartedAt)
		assert.True(t, pastStart.Equal(*s.StartedAt), "the competition starts as of its scheduled start")

		require.NoError(t, se.SetCompetitionStarted(false))
		assert.False(t, pendingStart(t, se), "a competition that was ever started is not started again")
	})

	t.Run("a scheduled start fires while the engine runs between rounds", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		se.NextRoundStartTime = time.Now().Add(time.Hour)
		start := at(1500 * time.Millisecond)
		require.NoError(t, se.SetCompetitionSchedule(start, nil))

		ready := waitInBackground(t, se)
		require.Eventually(t, pg.DB.GetCompetitionStarted, 2500*time.Millisecond, 20*time.Millisecond)
		assert.Less(t, time.Since(*start), time.Second, "the start is performed at the scheduled instant, not at the next round")
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		require.NotNil(t, s.StartedAt)
		assert.True(t, start.Equal(*s.StartedAt))
		expectBlocked(t, ready, 200*time.Millisecond)
	})

	t.Run("an expired schedule never starts the competition", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		require.True(t, se.SetPaused(true))
		require.NoError(t, se.SetCompetitionSchedule(at(-2*time.Hour), at(-time.Hour)))

		assert.False(t, pendingStart(t, se))
		expectBlocked(t, waitInBackground(t, se), 300*time.Millisecond)
		assert.False(t, pg.DB.GetCompetitionStarted())
		assert.True(t, se.Paused())
	})

	t.Run("a schedule written while waiting wakes the loop", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		require.True(t, se.SetPaused(true))
		ready := waitInBackground(t, se)
		expectBlocked(t, ready, 100*time.Millisecond)

		require.NoError(t, se.SetCompetitionSchedule(at(-time.Minute), nil))
		expectReady(t, ready, 2*time.Second)
		assert.True(t, pg.DB.GetCompetitionStarted())
		assert.False(t, se.Paused())
	})

	t.Run("a manual start before the scheduled start wins", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		require.True(t, se.SetPaused(true))
		require.NoError(t, se.SetCompetitionSchedule(at(time.Hour), nil))
		ready := waitInBackground(t, se)

		require.NoError(t, se.SetCompetitionStarted(true))
		assert.False(t, pendingStart(t, se))
		expectBlocked(t, ready, 200*time.Millisecond)
		assert.True(t, se.Paused(), "a manual start does not resume scoring")
	})

	t.Run("a pause during the inter-round sleep holds before the next round", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		se.NextRoundStartTime = time.Now().Add(400 * time.Millisecond)
		ready := waitInBackground(t, se)

		time.Sleep(50 * time.Millisecond)
		require.True(t, se.SetPaused(true))
		expectBlocked(t, ready, time.Second)

		require.True(t, se.SetPaused(false))
		expectReady(t, ready, time.Second)
	})

	t.Run("a scheduled start lifting an end-pause records no overtime", func(t *testing.T) {
		fresh(t)
		se := NewTestEngine(t, redis, pg, 3)
		se.Config.InjectSettings.End = at(-2 * time.Hour)
		s, err := se.CompetitionSchedule()
		require.NoError(t, err)
		require.True(t, se.pauseAtCompetitionEnd(s))

		require.NoError(t, se.SetCompetitionSchedule(at(-time.Minute), at(time.Hour)))
		expectReady(t, waitInBackground(t, se), 2*time.Second)
		assert.False(t, se.Paused())
		assert.True(t, pg.DB.GetCompetitionStarted())

		s, err = se.CompetitionSchedule()
		require.NoError(t, err)
		assert.False(t, se.pauseAtCompetitionEnd(s), "the new end is ahead")
		assert.Nil(t, se.pausedAtEnd, "the pause at the superseded end is forgotten")
		assert.Nil(t, overtime(t), "the old end was superseded, not resumed after")
	})
}
