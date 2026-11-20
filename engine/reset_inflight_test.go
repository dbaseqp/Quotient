package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResetScores_DiscardsRoundInFlight: an admin resets scores while a scored
// round is waiting for its slowest check. The reset must leave no scored round
// behind, so the engine re-enters preflight with zero points.
func TestResetScores_DiscardsRoundInFlight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	pg.ResetCompetitionState(t)
	require.NoError(t, pg.DB.ResetScores())
	redis.Client.Del(ctx, "tasks", "results")

	team := pg.CreateTestTeam(t, "Reset Inflight", "01")

	se := NewTestEngine(t, redis, pg, 3)
	se.Config.Current().MiscSettings.Delay = 30
	fast := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	slow := &mockRunner{Service: checks.Service{Display: "ssh", Name: "box01-ssh", ServiceType: "Mock", Points: 10}}
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: []checks.Runner{fast, slow}}}
	require.NoError(t, pg.DB.SetCompetitionStarted(true))

	slowStarted := make(chan struct{})
	release := make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }
	fakeRunner(t, redis, func(task Task) []checks.Result {
		if task.ServiceName == "box01-ssh" {
			startedOnce.Do(func() { close(slowStarted) })
			<-release
		}
		return passResult(task)
	})
	t.Cleanup(releaseSlow) // runs before fakeRunner's cleanup

	done := make(chan struct{})
	go func() {
		defer close(done)
		se.Start()
	}()

	select {
	case <-slowStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("slow check never reached the runner")
	}
	// Wait until the engine has collected the fast result and is blocked in
	// BLPop waiting for the slow one.
	require.Eventually(t, func() bool { return redis.Client.LLen(ctx, "results").Val() == 0 },
		5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Admin presses Reset (POST /api/engine/reset -> ScoringEngine.ResetScores).
	require.NoError(t, se.ResetScores())
	// The slow check finishes on the runner after the reset.
	releaseSlow()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return after the reset")
	}

	last, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, last.ID, "the round that was in flight during the reset was stored after it")
	points, _, _, err := pg.DB.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Zero(t, points, "points of the pre-reset round survive the reset")
	assert.False(t, pg.DB.GetCompetitionStarted(), "reset stops the competition")
	assert.True(t, se.PreflightPending(), "after a reset the engine must re-enter preflight, but a stored round makes it score rounds although the competition is stopped")
}
