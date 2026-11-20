package engine

import (
	"context"
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbBreakingRunner calls breakDB on its second Runnable call, i.e. after runChecks has built the
// first team's task and before it reads the second team's state.
type dbBreakingRunner struct {
	mockRunner
	calls   int
	breakDB func()
}

func (r *dbBreakingRunner) Runnable() bool {
	r.calls++
	if r.calls == 2 {
		r.breakDB()
	}
	return true
}

// Postgres going away while a round enqueues its checks (here between the first and second team)
// must fail the round with nothing queued and its ID kept; the retry stores every team's check.
func TestRvb_EnqueueReadFailureRetriesRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	require.NoError(t, redis.Client.Del(ctx, "tasks", "results").Err())
	require.NoError(t, pg.DB.ResetScores())
	pg.Exec(t, "UPDATE team_schemas SET active = false")
	pg.CreateTestTeam(t, "Enqueue Team", "01")
	pg.CreateTestTeam(t, "Enqueue Team", "02")

	se := NewTestEngine(t, redis, pg, 3)
	svc := &dbBreakingRunner{
		mockRunner: mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}},
		breakDB: func() {
			pg.Exec(t, "ALTER TABLE team_service_check_schemas RENAME TO team_service_check_schemas_away")
		},
	}
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: []checks.Runner{svc}}}
	t.Cleanup(func() {
		pg.Exec(t, `DO $$ BEGIN
			IF to_regclass('team_service_check_schemas_away') IS NOT NULL THEN
				ALTER TABLE team_service_check_schemas_away RENAME TO team_service_check_schemas;
			END IF; END $$`)
	})

	require.Error(t, se.rvb(), "a failed read while enqueueing must fail the round")
	queued, err := redis.Client.LLen(ctx, "tasks").Result()
	require.NoError(t, err)
	assert.Zero(t, queued, "the first team's check is not queued for the failed attempt")
	assert.Equal(t, uint(1), se.CurrentRound, "the round ID is not used up")
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID, "nothing is stored for the failed attempt")

	pg.Exec(t, "ALTER TABLE team_service_check_schemas_away RENAME TO team_service_check_schemas")
	fakeRunner(t, redis, nil)
	require.NoError(t, se.rvb())
	round, err = pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID, "the retry stores the same round ID")
	assert.Len(t, round.Checks, 2, "with every team's check")
}
