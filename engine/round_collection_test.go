package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectRunner pops tasks like runner/runner.go and hands each to handle in
// its own goroutine. Cleanup stops it with a sentinel task.
func collectRunner(t *testing.T, redis *testutil.RedisContainer, handle func(task Task, push func(checks.Result))) {
	t.Helper()
	ctx := context.Background()
	push := func(r checks.Result) {
		payload, _ := json.Marshal(r)
		redis.Client.RPush(ctx, "results", payload)
	}
	var wg sync.WaitGroup
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
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); handle(task, push) }()
		}
	}()
	t.Cleanup(func() {
		redis.Client.RPush(ctx, "tasks", "stop")
		<-finished
		wg.Wait()
		redis.Client.Del(ctx, "tasks", "results")
	})
}

func passed(task Task) checks.Result {
	return checks.Result{
		TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType,
		RoundID: task.RoundID, Preflight: task.Preflight, Status: true, Points: 10,
	}
}

// collectSetup starts from empty scores and queues with only the new teams active.
func collectSetup(t *testing.T, teams int, services ...string) (*ScoringEngine, *testutil.RedisContainer, *testutil.PostgresContainer, []db.TeamSchema) {
	t.Helper()
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, redis.Client.Del(context.Background(), "tasks", "results").Err())
	require.NoError(t, pg.DB.ResetScores())
	pg.Exec(t, "UPDATE team_schemas SET active = false")

	var created []db.TeamSchema
	for i := range teams {
		created = append(created, pg.CreateTestTeam(t, "Collect Team", string(rune('1'+i))))
	}
	se := NewTestEngine(t, redis, pg, 3)
	var runners []checks.Runner
	for _, s := range services {
		runners = append(runners, &mockRunner{Service: checks.Service{Display: s, Name: "box01-" + s, ServiceType: "Mock", Points: 10}})
	}
	se.Config.Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: runners}}
	return se, redis, pg, created
}

func lastRoundChecks(t *testing.T, pg *testutil.PostgresContainer, teamID uint) (db.RoundSchema, []db.ServiceCheckSchema) {
	t.Helper()
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	var mine []db.ServiceCheckSchema
	for _, c := range round.Checks {
		if c.TeamID == teamID {
			mine = append(mine, c)
		}
	}
	return round, mine
}

// A task whose result never comes back (runner crashed after BLPOP, result
// RPUSH failed) is left unscored; it must not erase every other team's
// collected results for the round, nor count against the team it belonged to.
func TestRvb_MissingResultIsUnscoredAndRoundKept(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 2, "web")
	up, lost := teams[0], teams[1]

	collectRunner(t, redis, func(task Task, push func(checks.Result)) {
		if task.TeamID == lost.ID {
			return
		}
		push(passed(task))
	})

	require.NoError(t, se.rvb())

	round, upChecks := lastRoundChecks(t, pg, up.ID)
	require.Equal(t, uint(1), round.ID, "round 1 is stored with the results that arrived")
	require.Len(t, upChecks, 1)
	assert.True(t, upChecks[0].Result)
	assert.Equal(t, 1, se.UptimePerService[up.ID]["box01-web"].TotalChecks)

	_, lostChecks := lastRoundChecks(t, pg, lost.ID)
	assert.Empty(t, lostChecks, "a lost result is not stored as a check")
	assert.Zero(t, se.UptimePerService[lost.ID]["box01-web"].TotalChecks, "nor counted in uptime")
	assert.Zero(t, se.SlaPerService[lost.ID]["box01-web"], "nor in the SLA streak")
}

// When no result at all arrives, the runners or Redis are down: the round is
// not scored, so an infrastructure outage costs no team points or SLAs.
func TestRvb_NoResultsAtAllStoresNoRound(t *testing.T) {
	se, redis, pg, _ := collectSetup(t, 2, "web")
	collectRunner(t, redis, func(Task, func(checks.Result)) {})

	require.NoError(t, se.rvb())

	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID)
	assert.Empty(t, se.SlaPerService)
}

// A result pushed shortly before the deadline is collected. BLPOP's timeout
// is whole seconds, which used to end collection up to a second early.
func TestRvb_CollectsResultPushedJustBeforeDeadline(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 1, "fast", "slow")

	collectRunner(t, redis, func(task Task, push func(checks.Result)) {
		if task.ServiceName == "box01-slow" {
			time.Sleep(time.Until(task.Deadline.Add(-400 * time.Millisecond)))
		}
		push(passed(task))
	})

	require.NoError(t, se.rvb())

	_, mine := lastRoundChecks(t, pg, teams[0].ID)
	require.Len(t, mine, 2)
	for _, c := range mine {
		assert.True(t, c.Result, "%s result arrived before the deadline", c.ServiceName)
	}
}

// After an engine restart mid-round the round ID is reused, and a check the
// previous process started can push a result with the same round ID. It must
// not be counted twice or take the place of another team's result.
func TestRvb_DuplicateSameRoundResultIsIgnored(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 2, "web")
	a, b := teams[0], teams[1]

	var once sync.Once
	collectRunner(t, redis, func(task Task, push func(checks.Result)) {
		once.Do(func() {
			push(checks.Result{TeamID: a.ID, ServiceName: "box01-web", ServiceType: "Mock",
				RoundID: task.RoundID, Status: true, Points: 10, Debug: "pre-restart run"})
		})
		push(passed(task))
	})

	require.NoError(t, se.rvb())

	_, aChecks := lastRoundChecks(t, pg, a.ID)
	_, bChecks := lastRoundChecks(t, pg, b.ID)
	assert.Len(t, aChecks, 1, "one row per team, service and round")
	require.Len(t, bChecks, 1)
	assert.True(t, bChecks[0].Result, "team %d's own result is stored", b.ID)
}
