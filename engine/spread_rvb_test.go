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

// TestRvb_SpreadKeepsTaskSetAndResults runs one round with spreading off and
// one with it on, against the same teams, services and service answers. Both
// rounds must enqueue the same tasks (one per team and service) and store the
// same pass/fail rows. The tasks differ only in Spread and in the start offset
// and finish budget the engine draws when spreading.
func TestRvb_SpreadKeepsTaskSetAndResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	teams := []db.TeamSchema{
		pg.CreateTestTeam(t, "Team Spread 1", "01"),
		pg.CreateTestTeam(t, "Team Spread 2", "02"),
		pg.CreateTestTeam(t, "Team Spread 3", "03"),
	}
	mine := map[uint]bool{}
	for _, tm := range teams {
		mine[tm.ID] = true
	}

	se := NewTestEngine(t, redis, pg, 3)
	se.CurrentRound = 1
	se.Config.Current().MiscSettings.Delay, se.Config.Current().MiscSettings.Jitter = 120, 10
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "10.0.0.1", Runners: []checks.Runner{
		&mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}},
		&mockRunner{Service: checks.Service{Display: "ssh", Name: "box01-ssh", ServiceType: "Mock", Points: 10}},
		&mockRunner{Service: checks.Service{Display: "dns", Name: "box01-dns", ServiceType: "Mock", Points: 10}},
	}}}

	type key struct {
		team    uint
		service string
	}
	var mu sync.Mutex
	seen := map[uint][]Task{} // round -> tasks for our teams
	fakeRunner(t, redis, func(task Task) []checks.Result {
		if mine[task.TeamID] {
			mu.Lock()
			seen[task.RoundID] = append(seen[task.RoundID], task)
			mu.Unlock()
		}
		res := passResult(task)
		// The same services are down in both rounds: ssh on team 2, dns on team 3.
		if (task.TeamID == teams[1].ID && task.ServiceName == "box01-ssh") || (task.TeamID == teams[2].ID && task.ServiceName == "box01-dns") {
			res[0].Status = false
			res[0].Error = "down"
		}
		return res
	})

	roundRows := func(spread *int) (map[key]bool, []Task) {
		se.Config.Current().MiscSettings.Spread = spread
		round := se.CurrentRound
		require.NoError(t, se.rvb())
		last, err := pg.DB.GetLastRound()
		require.NoError(t, err)
		require.Equal(t, round, uint(last.ID))
		rows := map[key]bool{}
		for _, c := range last.Checks {
			if mine[c.TeamID] {
				rows[key{c.TeamID, c.ServiceName}] = c.Result
			}
		}
		mu.Lock()
		defer mu.Unlock()
		return rows, seen[round]
	}
	off, on := 0, 60
	rowsOff, tasksOff := roundRows(&off)
	rowsOn, tasksOn := roundRows(&on)

	require.Len(t, rowsOff, 9, "one stored check per team and service")
	assert.Equal(t, rowsOff, rowsOn, "spreading changed which checks were stored or their results")
	assert.False(t, rowsOn[key{teams[1].ID, "box01-ssh"}])
	assert.False(t, rowsOn[key{teams[2].ID, "box01-dns"}])
	assert.True(t, rowsOn[key{teams[0].ID, "box01-web"}])

	taskSet := func(ts []Task) map[key]int {
		m := map[key]int{}
		for _, task := range ts {
			m[key{task.TeamID, task.ServiceName}]++
		}
		return m
	}
	assert.Equal(t, taskSet(tasksOff), taskSet(tasksOn), "spreading changed the round's task set")
	require.Len(t, tasksOn, 9)
	for _, task := range tasksOff {
		assert.Zero(t, task.Spread)
		assert.Zero(t, task.StartAfter, "spreading off: start at once")
		assert.Zero(t, task.FinishWithin)
	}
	for _, task := range tasksOn {
		assert.Equal(t, 60, task.Spread)
		assert.True(t, se.NextRoundStartTime.Equal(task.Deadline), "deadline unchanged: still the round's end")
		// The 110 s shortest round less SpreadMargin, less the time spent before the push.
		assert.LessOrEqual(t, task.FinishWithin, 110*time.Second-SpreadMargin)
		assert.Greater(t, task.FinishWithin, 100*time.Second-SpreadMargin)
		assert.LessOrEqual(t, task.StartAfter, 60*time.Second, "start offset capped by Spread")
		assert.LessOrEqual(t, task.StartAfter, task.FinishWithin, "the mock's 0 s worst case fits after the start")
	}
}

// slowMarshalRunner takes 300 ms to serialize the first time, like a round
// whose task building (DB lookups per team and service) takes a while.
type slowMarshalRunner struct {
	mockRunner
	slept bool
}

func (r *slowMarshalRunner) MarshalJSON() ([]byte, error) {
	if !r.slept {
		r.slept = true
		time.Sleep(300 * time.Millisecond)
	}
	return json.Marshal(r.mockRunner)
}

// TestRvb_SpreadWindowExcludesEnqueueTime: time the engine spends building a
// round's tasks comes out of the finish budget it sends, so a check started
// at the end of its window still finishes SpreadMargin before the shortest
// round ends.
func TestRvb_SpreadWindowExcludesEnqueueTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	team := pg.CreateTestTeam(t, "Team Slow Enqueue", "01")

	se := NewTestEngine(t, redis, pg, 3)
	se.CurrentRound = 1
	spread := 110
	misc := &se.Config.Current().MiscSettings
	misc.Delay, misc.Jitter, misc.Spread = 120, 10, &spread
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "10.0.0.1", Runners: []checks.Runner{
		&slowMarshalRunner{mockRunner: mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10, Timeout: 30, Attempts: 1}}},
	}}}

	var mu sync.Mutex
	var got []Task
	fakeRunner(t, redis, func(task Task) []checks.Result {
		if task.TeamID == team.ID {
			mu.Lock()
			got = append(got, task)
			mu.Unlock()
		}
		return passResult(task)
	})
	require.NoError(t, se.rvb())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 1)
	assert.LessOrEqual(t, got[0].FinishWithin, 110*time.Second-SpreadMargin-300*time.Millisecond, "building the task took 300 ms of the round")
	assert.LessOrEqual(t, got[0].StartAfter+30*time.Second, got[0].FinishWithin)
}

func TestGetActiveTasksListsScheduled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	se := NewTestEngine(t, redis, pg, 3)

	status, err := json.Marshal(checks.Result{TeamID: 1, ServiceName: "box01-web", RunnerID: "r1", StatusText: "scheduled"})
	require.NoError(t, err)
	require.NoError(t, redis.Client.Set(ctx, "task:false:1:1:Mock:box01-web", status, time.Minute).Err())

	tasks, err := se.GetActiveTasks()
	require.NoError(t, err)
	require.Len(t, tasks["scheduled"], 1)
	assert.Equal(t, "r1", tasks["scheduled"].([]any)[0].(checks.Result).RunnerID)
	assert.Equal(t, []any{"r1"}, tasks["all_runners"])
}
