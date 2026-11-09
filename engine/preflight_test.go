package engine

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// passResult is the result fakeRunner pushes for a task when respond is nil.
func passResult(task Task) []checks.Result {
	return []checks.Result{{
		TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
		RoundID: task.RoundID, Preflight: task.Preflight, Status: true, Points: 10,
	}}
}

// fakeRunner answers every task on the "tasks" queue with respond(task) until
// stop is called. respond defaults to passResult. go-redis does not interrupt
// a blocking BLPop on context cancellation, so stop pushes a sentinel task that
// the goroutine consumes before it checks done.
func fakeRunner(t *testing.T, redis *testutil.RedisContainer, respond func(Task) []checks.Result) (stop func()) {
	t.Helper()
	if respond == nil {
		respond = passResult
	}
	ctx := context.Background()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			val, err := redis.Client.BLPop(ctx, 0, "tasks").Result()
			select {
			case <-done:
				return
			default:
			}
			if err != nil || len(val) < 2 {
				continue
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
	return func() {
		close(done)
		redis.Client.RPush(ctx, "tasks", "stop")
		<-finished
	}
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

	var scoredTasks atomic.Int64
	stop := fakeRunner(t, redis, func(task Task) []checks.Result {
		if !task.Preflight {
			scoredTasks.Add(1)
		}
		return passResult(task)
	})
	require.NoError(t, se.preflight())
	require.NoError(t, se.preflight())
	stop()

	assert.Zero(t, scoredTasks.Load(), "preflight tasks are marked for the runner")

	round, rows := teamPreflight(t, pg, team.ID)
	assert.Equal(t, uint(2), round.ID, "only the latest preflight round is returned")
	assert.Equal(t, se.CurrentRoundStartTime.Unix(), round.StartTime.Unix())
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

	stop := fakeRunner(t, redis, func(task Task) []checks.Result {
		scored := checks.Result{
			TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
			RoundID: task.RoundID, Preflight: false, Status: false,
		}
		return append([]checks.Result{scored}, passResult(task)...)
	})
	require.NoError(t, se.preflight())
	stop()

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

func TestCompetitionEverStarted_StaysStartedAfterStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	t.Cleanup(func() { require.NoError(t, pg.DB.ResetScores()) })

	started, err := pg.DB.CompetitionEverStarted()
	require.NoError(t, err)
	assert.False(t, started)

	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	started, err = pg.DB.CompetitionEverStarted()
	require.NoError(t, err)
	assert.True(t, started)

	require.NoError(t, pg.DB.SetCompetitionStarted(false))
	started, err = pg.DB.CompetitionEverStarted()
	require.NoError(t, err)
	assert.True(t, started, "stopping after the first start doesn't return to preflight")
}

func TestCompetitionEverStarted_LegacyRowWithoutStartedAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	t.Cleanup(func() { require.NoError(t, pg.DB.ResetScores()) })

	// Rows written before StartedAt existed: the UI treats them as started, so
	// the engine must not run preflight rounds against them.
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	pg.Exec(t, "UPDATE competition_state_schemas SET started_at = NULL")

	started, err := pg.DB.CompetitionEverStarted()
	require.NoError(t, err)
	assert.True(t, started)
	assert.True(t, pg.DB.GetCompetitionStarted())
}

func TestResetScores_ReentersPreflight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	t.Cleanup(func() { require.NoError(t, pg.DB.ResetScores()) })

	pg.CreateTestTeam(t, "Team Preflight Reset", "01")
	se := newPreflightEngine(t, redis, pg)

	stop := fakeRunner(t, redis, nil)
	require.NoError(t, se.preflight())
	stop()
	require.NoError(t, pg.DB.SetCompetitionStarted(true))

	require.NoError(t, se.ResetScores())

	started, err := pg.DB.CompetitionEverStarted()
	require.NoError(t, err)
	assert.False(t, started, "reset returns the engine to preflight rounds")
	round, err := pg.DB.GetLatestPreflightRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID, "reset drops the previous event's preflight rounds")
}
