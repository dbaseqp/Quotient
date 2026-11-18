package engine

import (
	"context"
	"sync"
	"testing"

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
// same pass/fail rows. Only the Spread the runners receive differs.
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
		assert.True(t, task.StartAt.IsZero(), "spreading off: start at once")
	}
	for _, task := range tasksOn {
		assert.Equal(t, 60, task.Spread)
		assert.True(t, se.NextRoundStartTime.Equal(task.Deadline), "deadline unchanged: still the round's end")
		assert.False(t, task.StartAt.IsZero(), "spreading on: the engine draws each task's start")
		assert.False(t, task.StartAt.After(task.Deadline.Add(-SpreadMargin)), "start %s leaves no margin before the deadline %s", task.StartAt, task.Deadline)
	}
}
