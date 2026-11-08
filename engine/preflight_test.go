package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func answerTasks(t *testing.T, redis *testutil.RedisContainer, seen chan<- Task, extra func(Task) []checks.Result) (stop func()) {
	t.Helper()
	ctx := context.Background()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			default:
			}
			val, err := redis.Client.BLPop(ctx, time.Second, "tasks").Result()
			if err != nil || len(val) < 2 {
				continue
			}
			var task Task
			if err := json.Unmarshal([]byte(val[1]), &task); err != nil {
				continue
			}
			if seen != nil {
				seen <- task
			}
			results := []checks.Result{}
			if extra != nil {
				results = extra(task)
			}
			results = append(results, checks.Result{
				TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
				RoundID: task.RoundID, Preflight: task.Preflight, Status: true, Points: 10,
			})
			for _, r := range results {
				payload, _ := json.Marshal(r)
				redis.Client.RPush(ctx, "results", payload)
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

func newPreflightEngine(t *testing.T, redis *testutil.RedisContainer, pg *testutil.PostgresContainer) *ScoringEngine {
	t.Helper()
	se := NewTestEngine(t, redis, pg, 3)
	last, err := pg.DB.GetLastPreflightRound()
	require.NoError(t, err)
	se.PreflightRound = last
	svc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	se.Config.Box = []config.Box{{Name: "box01", IP: "10.0.0.1", Runners: []checks.Runner{svc}}}
	return se
}

func teamPreflight(t *testing.T, pg *testutil.PostgresContainer, teamID uint) []db.PreflightCheckSchema {
	t.Helper()
	rows, err := pg.DB.GetLatestPreflightChecks()
	require.NoError(t, err)
	var mine []db.PreflightCheckSchema
	for _, row := range rows {
		if row.TeamID == teamID {
			mine = append(mine, row)
		}
	}
	return mine
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
	firstRound := se.PreflightRound + 1

	seen := make(chan Task, 100)
	stop := answerTasks(t, redis, seen, nil)
	require.NoError(t, se.preflight())
	require.NoError(t, se.preflight())
	stop()
	close(seen)

	for task := range seen {
		assert.True(t, task.Preflight, "preflight tasks are marked for the runner")
	}

	assert.Equal(t, firstRound+1, se.PreflightRound)
	rows := teamPreflight(t, pg, team.ID)
	require.Len(t, rows, 1, "only the latest preflight round is returned")
	assert.Equal(t, firstRound+1, rows[0].RoundID)
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

	stop := answerTasks(t, redis, nil, func(task Task) []checks.Result {
		return []checks.Result{{
			TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
			RoundID: task.RoundID, Preflight: false, Status: false,
		}}
	})
	require.NoError(t, se.preflight())
	stop()

	rows := teamPreflight(t, pg, team.ID)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Result, "a scored round's result must not be stored as preflight")
}

func TestCompetitionStarted_StaysStartedAfterStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	t.Cleanup(func() { pg.ResetCompetitionState(t) })
	se := NewTestEngine(t, redis, pg, 3)

	assert.False(t, se.competitionEverStarted())
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	assert.True(t, se.competitionEverStarted())
	require.NoError(t, pg.DB.SetCompetitionStarted(false))
	assert.True(t, se.competitionEverStarted(), "stopping after the first start doesn't return to preflight")
}
