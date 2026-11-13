package engine

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func passResult(task Task) []checks.Result {
	return []checks.Result{{
		TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
		RoundID: task.RoundID, Preflight: task.Preflight, Status: true, Points: 10,
	}}
}

// fakeRunner answers every task on the "tasks" queue with respond(task) until
// test cleanup. go-redis does not interrupt a blocking BLPop on context
// cancellation, so cleanup pushes a "stop" sentinel instead.
func fakeRunner(t *testing.T, redis *testutil.RedisContainer, respond func(Task) []checks.Result) {
	t.Helper()
	if respond == nil {
		respond = passResult
	}
	ctx := context.Background()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			val, err := redis.Client.BLPop(ctx, 0, "tasks").Result()
			if err != nil || len(val) < 2 {
				continue
			}
			if val[1] == "stop" {
				return
			}
			var task Task
			if err := json.Unmarshal([]byte(val[1]), &task); err != nil {
				t.Logf("Failed to unmarshal task: %v", err)
				continue
			}
			for _, r := range respond(task) {
				payload, _ := json.Marshal(r)
				redis.Client.RPush(ctx, "results", payload)
			}
		}
	}()
	t.Cleanup(func() {
		redis.Client.RPush(ctx, "tasks", "stop")
		<-finished
	})
}

func newPreflightEngine(t *testing.T, redis *testutil.RedisContainer, pg *testutil.PostgresContainer) *ScoringEngine {
	t.Helper()
	se := NewTestEngine(t, redis, pg, 3)
	svc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	se.Config.Box = []config.Box{{Name: "box01", IP: "10.0.0.1", Runners: []checks.Runner{svc}}}
	return se
}

func teamPreflight(t *testing.T, pg *testutil.PostgresContainer, teamID uint) (db.PreflightRoundSchema, []db.PreflightCheckSchema) {
	t.Helper()
	round, err := pg.DB.GetLatestPreflightRound()
	require.NoError(t, err)
	var mine []db.PreflightCheckSchema
	for _, row := range round.Checks {
		if row.TeamID == teamID {
			mine = append(mine, row)
		}
	}
	return round, mine
}

func TestPreflight_StoresResultsApartFromScoring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team Preflight", "01")
	se := newPreflightEngine(t, redis, pg)

	events := redis.Client.Subscribe(ctx, "events")
	defer events.Close() //nolint:errcheck
	_, err := events.Receive(ctx)
	require.NoError(t, err)
	eventsCh := events.Channel()

	var scoredTasks atomic.Int64
	fakeRunner(t, redis, func(task Task) []checks.Result {
		if !task.Preflight {
			scoredTasks.Add(1)
		}
		return passResult(task)
	})
	se.CurrentRoundStartTime = time.Now().Truncate(time.Microsecond)
	require.NoError(t, se.preflight())
	require.NoError(t, se.preflight())

	for range 2 {
		select {
		case msg := <-eventsCh:
			assert.Equal(t, "round_finish", msg.Payload, "preflight rounds publish round_finish for divisor")
		case <-time.After(time.Second):
			t.Fatal("expected round_finish event after preflight round")
		}
	}

	assert.Zero(t, scoredTasks.Load(), "preflight tasks are marked for the runner")

	round, rows := teamPreflight(t, pg, team.ID)
	assert.Equal(t, uint(2), round.ID, "only the latest preflight round is returned")
	assert.True(t, se.CurrentRoundStartTime.Equal(round.StartTime), "round start time %s stored as %s", se.CurrentRoundStartTime, round.StartTime)
	require.Len(t, rows, 1)
	assert.Equal(t, round.ID, rows[0].RoundID)
	assert.True(t, rows[0].Result)
	assert.Equal(t, "box01-web", rows[0].ServiceName)
	assert.Equal(t, "Mock", rows[0].ServiceType)
	assert.Equal(t, team.Name, rows[0].Team.Name)

	lastRound, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, lastRound.ID, "preflight rounds never create scored rounds")
	assert.Equal(t, uint(1), se.CurrentRound, "preflight rounds don't advance the scored round")
	assert.Empty(t, se.UptimePerService, "preflight rounds don't count toward uptime")
	assert.Empty(t, se.SlaPerService, "preflight rounds don't count toward SLAs")
}

func TestPreflight_IgnoresScoredRoundResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team Preflight Mixed", "01")
	se := newPreflightEngine(t, redis, pg)

	fakeRunner(t, redis, func(task Task) []checks.Result {
		scored := checks.Result{
			TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
			RoundID: task.RoundID, Preflight: false, Status: false,
		}
		return append([]checks.Result{scored}, passResult(task)...)
	})
	require.NoError(t, se.preflight())

	_, rows := teamPreflight(t, pg, team.ID)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Result, "a scored round's result must not be stored as preflight")
}

func TestPreflight_StoresRoundWithoutResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	pg.CreateTestTeam(t, "Team Preflight Timeout", "01")
	se := newPreflightEngine(t, redis, pg)
	se.Config.MiscSettings.Delay = 2

	// No runner answers, so the round times out with nothing collected.
	require.NoError(t, se.preflight())

	round, err := pg.DB.GetLatestPreflightRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID, "a round that collected nothing is still the latest round")
	assert.Empty(t, round.Checks)
}

func TestPreflightPending_StopReturnsToPreflightOnlyBeforeScoring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)
	se := newPreflightEngine(t, redis, pg)

	assert.True(t, se.PreflightPending())

	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	assert.False(t, se.PreflightPending())

	_, err := pg.DB.CreateRound(db.RoundSchema{ID: 1, StartTime: time.Now()})
	require.NoError(t, err)
	require.NoError(t, pg.DB.SetCompetitionStarted(false))
	assert.False(t, se.PreflightPending(), "stopping after a scored round doesn't return to preflight")

	require.NoError(t, pg.DB.ResetScores())
	assert.True(t, se.PreflightPending(), "stopping before any scored round returns to preflight")
}

func TestPreflightPending_IgnoresRoundCounterAfterEmptyRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)

	pg.CreateTestTeam(t, "Team Preflight Empty", "01")
	se := newPreflightEngine(t, redis, pg)

	// A round that stored nothing still advances the in-memory counter.
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	se.finishScoredRound()
	assert.Equal(t, uint(2), se.CurrentRound)
	last, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	require.Zero(t, last.ID, "a round without results is not stored")

	require.NoError(t, pg.DB.SetCompetitionStarted(false))
	assert.True(t, se.PreflightPending(), "stopping before any scored round was stored returns to preflight")
}

func TestPreflightPending_LegacyRowWithoutStartedAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)
	se := newPreflightEngine(t, redis, pg)

	// Rows written before StartedAt existed: the UI treats them as started, so
	// the engine must not run preflight rounds against them.
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	pg.Exec(t, "UPDATE competition_state_schemas SET started_at = NULL")

	assert.False(t, se.PreflightPending())
}

func TestResetScores_ReentersPreflight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)

	pg.CreateTestTeam(t, "Team Preflight Reset", "01")
	se := newPreflightEngine(t, redis, pg)

	fakeRunner(t, redis, nil)
	require.NoError(t, se.preflight())
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	startedAt, err := pg.DB.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, startedAt)
	require.NoError(t, se.rvb())
	assert.False(t, se.PreflightPending())

	require.NoError(t, pg.DB.SetCompetitionStarted(false))
	assert.False(t, se.PreflightPending(), "stopping alone doesn't drop the scored round")
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	require.NoError(t, se.ResetScores())
	assert.False(t, pg.DB.GetCompetitionStarted(), "reset stops the competition")
	assert.True(t, se.PreflightPending(), "reset returns the engine to preflight rounds")
	afterReset, err := pg.DB.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, afterReset, "reset keeps the competition start, the anchor of imported injects")
	assert.True(t, startedAt.Equal(*afterReset))
	round, err := pg.DB.GetLatestPreflightRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID, "reset drops the previous event's preflight rounds")
}
