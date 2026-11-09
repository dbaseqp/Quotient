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

func TestPause_RacingTogglesWithAWaitingLoop(t *testing.T) {
	se := &ScoringEngine{Config: &config.ConfigSettings{}}
	se.resetPauseState()

	stop := make(chan struct{})
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			se.pauseMu.Lock()
			resume := se.resume
			se.pauseMu.Unlock()
			select {
			case <-resume:
			case <-stop:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 2000 {
				se.SetPaused(i%2 == 0)
			}
		}()
	}
	wg.Wait()

	se.SetPaused(false)
	se.pauseMu.Lock()
	resume := se.resume
	se.pauseMu.Unlock()
	select {
	case <-resume:
	case <-time.After(time.Second):
		t.Fatal("a resume left the round loop blocked")
	}
	close(stop)
	<-loopDone
}

func TestStart_ExitsCleanlyOnResetWhilePaused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	se := NewTestEngine(t, redis, pg, 3)
	require.True(t, se.SetPaused(true))

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
		t.Fatal("Start did not return after ResetScores while paused")
	}
	assert.True(t, se.Paused(), "pause state set before Start is preserved")
}
