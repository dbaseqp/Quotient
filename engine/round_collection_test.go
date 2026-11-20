package engine

import (
	"context"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectSetup starts from empty scores and queues with only the new teams active.
func collectSetup(t *testing.T, teams int, services ...string) (*ScoringEngine, *testutil.RedisContainer, *testutil.PostgresContainer, []db.TeamSchema) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
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
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: runners}}
	return se, redis, pg, created
}

func lastRoundChecks(t *testing.T, pg *testutil.PostgresContainer, teamID uint) []db.ServiceCheckSchema {
	t.Helper()
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	var mine []db.ServiceCheckSchema
	for _, c := range round.Checks {
		if c.TeamID == teamID {
			mine = append(mine, c)
		}
	}
	return mine
}

// A task whose result never comes back (runner crashed after BLPOP, result
// RPUSH failed) discards the whole round: if one team can't be scored, no
// team is, so the team whose result did arrive is not scored either.
func TestRvb_MissingResultDiscardsRoundForEveryTeam(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 2, "web")
	up, lost := teams[0], teams[1]

	fakeRunner(t, redis, func(task Task) []checks.Result {
		if task.TeamID == lost.ID {
			return nil
		}
		return passResult(task)
	})

	require.NoError(t, se.rvb())

	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Zero(t, round.ID, "no round is stored")
	assert.Zero(t, se.UptimePerService[up.ID]["box01-web"].TotalChecks, "the reporting team is not scored")
	assert.Empty(t, se.SlaPerService)
}

// Preflight results never count, so a missing result does not discard the
// results that did arrive.
func TestPreflight_MissingResultKeepsOtherTeamsResults(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 2, "web")
	up, lost := teams[0], teams[1]

	fakeRunner(t, redis, func(task Task) []checks.Result {
		if task.TeamID == lost.ID {
			return nil
		}
		return passResult(task)
	})

	require.NoError(t, se.preflight())

	_, upRows := teamPreflight(t, pg, up.ID)
	_, lostRows := teamPreflight(t, pg, lost.ID)
	require.Len(t, upRows, 1)
	assert.True(t, upRows[0].Result)
	assert.Empty(t, lostRows)
}

// A task the engine cannot build (here the service-state lookup fails) never
// reports, so the round is incomplete rather than scored without it.
func TestRunChecks_TaskThatCannotBeBuiltLeavesRoundIncomplete(t *testing.T) {
	se, _, pg, _ := collectSetup(t, 1, "web")
	pg.Exec(t, "ALTER TABLE team_service_check_schemas RENAME TO team_service_check_schemas_broken")
	t.Cleanup(func() {
		pg.Exec(t, "ALTER TABLE team_service_check_schemas_broken RENAME TO team_service_check_schemas")
	})

	_, complete, err := se.runChecks(se.Config.Current(), 1, false)
	require.NoError(t, err)
	assert.False(t, complete)
}

// A result pushed shortly before the deadline is collected, and so is the
// timeout result a runner pushes at the deadline for a check still running.
func TestRvb_CollectsResultsUpToDeadline(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 1, "fast", "slow", "hung")

	fakeRunner(t, redis, func(task Task) []checks.Result {
		r := passResult(task)
		switch task.ServiceName {
		case "box01-slow":
			time.Sleep(time.Until(task.Deadline.Add(-400 * time.Millisecond)))
		case "box01-hung":
			time.Sleep(time.Until(task.Deadline))
			r[0].Status, r[0].Points, r[0].Error = false, 0, "timeout"
		}
		return r
	})

	require.NoError(t, se.rvb())

	mine := lastRoundChecks(t, pg, teams[0].ID)
	require.Len(t, mine, 3)
	for _, c := range mine {
		if c.ServiceName == "box01-hung" {
			assert.False(t, c.Result)
			assert.Equal(t, "timeout", c.Error)
			continue
		}
		assert.True(t, c.Result, "%s result arrived before the deadline", c.ServiceName)
	}
}

// After an engine restart mid-round the round ID is reused, and a check the
// previous process started can push a result with the same round ID. It must
// not be counted twice or take the place of another team's result.
func TestRvb_DuplicateSameRoundResultIsIgnored(t *testing.T) {
	se, redis, pg, teams := collectSetup(t, 2, "web")

	// The runner answers tasks in order, so the stray result for the first
	// task's team lands right before that team's real result.
	first := true
	fakeRunner(t, redis, func(task Task) []checks.Result {
		r := passResult(task)
		if first {
			first = false
			stray := r[0]
			stray.Debug = "pre-restart run"
			r = append([]checks.Result{stray}, r...)
		}
		return r
	})

	require.NoError(t, se.rvb())

	for _, team := range teams {
		mine := lastRoundChecks(t, pg, team.ID)
		require.Len(t, mine, 1, "one row for team %d's service in the round", team.ID)
		assert.True(t, mine[0].Result)
	}
}
