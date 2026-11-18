package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForRetryWaitsDelay(t *testing.T) {
	start := time.Now()
	assert.True(t, waitForRetry(context.Background(), 50*time.Millisecond, start.Add(time.Second)))
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
}

func TestWaitForRetrySkipsWhenDeadlineTooClose(t *testing.T) {
	start := time.Now()
	assert.False(t, waitForRetry(context.Background(), time.Second, start.Add(500*time.Millisecond)))
	assert.Less(t, time.Since(start), 100*time.Millisecond, "should not sleep when no retry fits")
}

func TestWaitForRetryStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, waitForRetry(ctx, time.Second, time.Now().Add(time.Minute)))
}

// stalledCheck never answers, like a check still running when its round ends.
type stalledCheck struct{ checks.Service }

func (c *stalledCheck) Run(uint, string, uint, chan checks.Result) {}

func (c *stalledCheck) Verify(string, string, int, int, int, int) error { return nil }

func TestHandleTaskKeepsPointsWhenRoundEndsFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()
	require.NoError(t, rdb.Client.Del(ctx, "results").Err())

	task := &engine.Task{
		TeamID: 1, ServiceType: "Stalled", ServiceName: "box-stalled", RoundID: 1,
		Attempts: 1, Deadline: time.Now().Add(100 * time.Millisecond),
	}
	handleTask(ctx, rdb.Client, &stalledCheck{checks.Service{Points: 7}}, task)

	raw, err := rdb.Client.LPop(ctx, "results").Result()
	require.NoError(t, err)
	var result checks.Result
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	assert.False(t, result.Status)
	assert.Equal(t, "timeout", result.Error)
	assert.Equal(t, 7, result.Points, "a failed check still counts toward the service's possible points")
}

func TestRetryDelayWithoutSpreadIsAttemptDelay(t *testing.T) {
	now := time.Now()
	task := &engine.Task{Attempts: 2, AttemptDelay: 45, Deadline: now.Add(120 * time.Second)}
	assert.Equal(t, 45*time.Second, retryDelay(now, task, 30, 1))
}

func TestRetryDelayStaysInBudget(t *testing.T) {
	now := time.Now()
	task := &engine.Task{Attempts: 3, AttemptDelay: 10, Spread: 120, Deadline: now.Add(120 * time.Second)}
	// After the first of 3 attempts, 2 remain: 20 + 10 + 20 = 50 s worst case.
	remainingWorst := 50 * time.Second
	var extra bool
	for range 500 {
		d := retryDelay(now, task, 20, 1)
		require.GreaterOrEqual(t, d, 10*time.Second, "a retry never comes sooner than AttemptDelay")
		require.LessOrEqual(t, d+remainingWorst+engine.SpreadMargin, 120*time.Second, "the remaining attempts still fit")
		extra = extra || d > 11*time.Second
	}
	assert.True(t, extra, "spread retries wait past AttemptDelay")
}

// scriptedCheck fails its first failFirst attempts and then passes, or fails
// every attempt when failFirst < 0. It records when each attempt started.
type scriptedCheck struct {
	checks.Service
	failFirst int
	mu        sync.Mutex
	starts    []time.Time
}

func (c *scriptedCheck) Run(teamID uint, _ string, roundID uint, out chan checks.Result) {
	c.mu.Lock()
	c.starts = append(c.starts, time.Now())
	n := len(c.starts)
	c.mu.Unlock()
	r := checks.Result{TeamID: teamID, RoundID: roundID, Status: c.failFirst >= 0 && n > c.failFirst}
	if !r.Status {
		r.Error = "scripted failure"
	}
	out <- r
}

func (c *scriptedCheck) Verify(string, string, int, int, int, int) error { return nil }

// TestHandleTaskSpreadKeepsResults runs the same scripted checks with spread
// off and on: the result, its error and the number of attempts must match,
// the first attempt must start early enough for the worst case to fit, and
// every result must arrive before the deadline.
func TestHandleTaskSpreadKeepsResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()

	type outcome struct {
		status   bool
		error    string
		attempts int
	}
	run := func(name string, failFirst, spread int) outcome {
		require.NoError(t, rdb.Client.Del(ctx, "results").Err())
		check := &scriptedCheck{Service: checks.Service{Timeout: 1, Points: 3}, failFirst: failFirst}
		deadline := time.Now().Add(6 * time.Second)
		task := &engine.Task{
			TeamID: 1, ServiceType: "Scripted", ServiceName: name, RoundID: 1,
			Attempts: 2, AttemptDelay: 1, Spread: spread, Deadline: deadline,
		}
		handleTask(ctx, rdb.Client, check, task)
		require.False(t, time.Now().After(deadline), "%s: finished after the deadline", name)

		raw, err := rdb.Client.LPop(ctx, "results").Result()
		require.NoError(t, err)
		var result checks.Result
		require.NoError(t, json.Unmarshal([]byte(raw), &result))
		require.Equal(t, name, result.ServiceName)
		require.NotEmpty(t, check.starts)
		// 2 attempts of 1 s with 1 s between them: 3 s worst case.
		latest := deadline.Add(-3*time.Second - engine.SpreadMargin)
		assert.False(t, check.starts[0].After(latest.Add(50*time.Millisecond)), "%s: first attempt started too late", name)
		return outcome{result.Status, result.Error, len(check.starts)}
	}

	for _, tc := range []struct {
		name      string
		failFirst int
		want      outcome
	}{
		{"pass", 0, outcome{true, "", 1}},
		{"pass-on-retry", 1, outcome{true, "", 2}},
		{"fail", -1, outcome{false, "scripted failure", 2}},
	} {
		off := run("spread-off-"+tc.name, tc.failFirst, 0)
		on := run("spread-on-"+tc.name, tc.failFirst, 60)
		assert.Equal(t, tc.want, off, tc.name)
		assert.Equal(t, off, on, "%s: spreading changed the outcome", tc.name)
	}
}
