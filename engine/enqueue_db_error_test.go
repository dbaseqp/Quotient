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

// A DB read that fails while a round enqueues its checks (Postgres restarting at round start) must
// fail the round so the loop retries it under the same ID. Skipping the affected checks stored the
// round without them, so teams lost those checks' points for an infrastructure blip.
func TestRvb_EnqueueReadFailureRetriesRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, redis.Client.Del(context.Background(), "tasks", "results").Err())
	require.NoError(t, pg.DB.ResetScores())
	pg.Exec(t, "UPDATE team_schemas SET active = false")
	pg.CreateTestTeam(t, "Enqueue Team", "01")
	pg.CreateTestTeam(t, "Enqueue Team", "02")

	se := NewTestEngine(t, redis, pg, 3)
	svc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	se.Config.Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: []checks.Runner{svc}}}
	fakeRunner(t, redis, nil)

	pg.Exec(t, "ALTER TABLE team_service_check_schemas RENAME TO team_service_check_schemas_away")
	t.Cleanup(func() {
		pg.Exec(t, `DO $$ BEGIN
			IF to_regclass('team_service_check_schemas_away') IS NOT NULL THEN
				ALTER TABLE team_service_check_schemas_away RENAME TO team_service_check_schemas;
			END IF; END $$`)
	})

	require.Error(t, se.rvb(), "a failed read while enqueueing must fail the round")
	assert.Equal(t, uint(1), se.CurrentRound, "the round ID is not used up")
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID, "nothing is stored for the failed attempt")

	pg.Exec(t, "ALTER TABLE team_service_check_schemas_away RENAME TO team_service_check_schemas")
	require.NoError(t, se.rvb())
	round, err = pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID, "the retry stores the same round ID")
	assert.Len(t, round.Checks, 2, "with every team's check")
}
