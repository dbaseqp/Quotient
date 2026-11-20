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
	assert.Equal(t, 45*time.Second, retryDelay(task, 30, 1, now, now))
}

// TestRetryDelayFitsFinishWithin: retries draw their extra from the finish
// budget the engine sent, measured from pickup, not from the later deadline.
func TestRetryDelayFitsFinishWithin(t *testing.T) {
	now := time.Now()
	picked := now.Add(-20 * time.Second)
	task := &engine.Task{Attempts: 3, AttemptDelay: 10, Spread: 120, FinishWithin: 90 * time.Second, Deadline: now.Add(120 * time.Second)}
	// After the first of 3 attempts, 2 remain: 20 + 10 + 20 = 50 s worst case,
	// and 90 - 20 = 70 s of the budget is left.
	var extra bool
	for range 500 {
		d := retryDelay(task, 20, 1, picked, now)
		require.GreaterOrEqual(t, d, 10*time.Second, "a retry never comes sooner than AttemptDelay")
		require.LessOrEqual(t, d+50*time.Second, 70*time.Second, "the remaining attempts end within FinishWithin")
		extra = extra || d > 11*time.Second
	}
	assert.True(t, extra, "spread retries wait past AttemptDelay")
}

// TestFirstStartFitsDeadline: a task popped late, with less of its round left
// than StartAfter, starts early enough that its worst case still ends
// SpreadMargin before the deadline.
func TestFirstStartFitsDeadline(t *testing.T) {
	now := time.Now()
	task := &engine.Task{
		Attempts: 2, AttemptDelay: 5, Spread: 120,
		StartAfter: 72 * time.Second, FinishWithin: 100 * time.Second, Deadline: now.Add(40 * time.Second),
	}
	// 2 attempts of 10 s with 5 s between them: 25 s worst case.
	start := firstStart(task, 10, now, now)
	assert.False(t, start.Before(now))
	assert.False(t, start.Add(25*time.Second).After(task.Deadline.Add(-engine.SpreadMargin)), "worst case ends by Deadline - SpreadMargin")
}

// TestRetryDelayFitsDeadline: when the deadline is closer than FinishWithin,
// retry extras still leave the remaining attempts SpreadMargin before it.
func TestRetryDelayFitsDeadline(t *testing.T) {
	now := time.Now()
	task := &engine.Task{Attempts: 3, AttemptDelay: 10, Spread: 120, FinishWithin: 90 * time.Second, Deadline: now.Add(80 * time.Second)}
	for range 500 {
		d := retryDelay(task, 20, 1, now, now)
		require.LessOrEqual(t, now.Add(d+50*time.Second), task.Deadline.Add(-engine.SpreadMargin), "the remaining attempts still fit")
	}
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
// a spread task's first attempt must wait StartAfter from pickup, and every
// result must arrive within FinishWithin (spread) or before the deadline.
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
		picked := time.Now()
		// 2 attempts of 1 s with 1 s between them: 3 s worst case after a 1 s start offset.
		finish := picked.Add(4 * time.Second)
		task := &engine.Task{
			TeamID: 1, ServiceType: "Scripted", ServiceName: name, RoundID: 1,
			Attempts: 2, AttemptDelay: 1, Spread: spread, Deadline: finish.Add(engine.SpreadMargin),
		}
		if spread > 0 {
			task.StartAfter, task.FinishWithin = time.Second, 4*time.Second
		}
		handleTask(ctx, rdb.Client, check, task)
		if spread > 0 {
			require.False(t, time.Now().After(finish.Add(100*time.Millisecond)), "%s: finished after FinishWithin", name)
		}
		require.False(t, time.Now().After(task.Deadline), "%s: finished after the deadline", name)

		raw, err := rdb.Client.LPop(ctx, "results").Result()
		require.NoError(t, err)
		var result checks.Result
		require.NoError(t, json.Unmarshal([]byte(raw), &result))
		require.Equal(t, name, result.ServiceName)
		require.NotEmpty(t, check.starts)
		assert.False(t, check.starts[0].Before(picked.Add(task.StartAfter)), "%s: first attempt started before StartAfter", name)
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

// TestHandleTaskMarksScheduled: while a spread task waits for its start, its
// task key says which runner holds it and when it starts; once it ran, the
// key holds its result.
func TestHandleTaskMarksScheduled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()
	key := "task:false:1:1:Scripted:box-scheduled"
	require.NoError(t, rdb.Client.Del(ctx, "results", key).Err())
	defer func(id string) { runnerID = id }(runnerID)
	runnerID = "runner-scheduled-test"

	task := &engine.Task{
		TeamID: 1, ServiceType: "Scripted", ServiceName: "box-scheduled", RoundID: 1, Attempts: 1,
		Spread: 60, StartAfter: time.Minute, FinishWithin: 2 * time.Minute, Deadline: time.Now().Add(3 * time.Minute),
	}
	check := &scriptedCheck{Service: checks.Service{Timeout: 1, Points: 3}}
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTask(waitCtx, rdb.Client, check, task)
	}()

	var status checks.Result
	require.Eventually(t, func() bool {
		raw, err := rdb.Client.Get(ctx, key).Result()
		return err == nil && json.Unmarshal([]byte(raw), &status) == nil
	}, 10*time.Second, 10*time.Millisecond, "no task key while the task waits for its start")
	cancel()
	<-done
	assert.Equal(t, checks.StatusScheduled, status.StatusText)
	assert.Equal(t, "runner-scheduled-test", status.RunnerID)
	assert.NotEmpty(t, status.StartTime)
	assert.Positive(t, rdb.Client.TTL(ctx, key).Val(), "the key expires with the round")
	assert.Empty(t, check.starts, "the check must not run before its start")

	task.StartAfter = time.Millisecond
	handleTask(ctx, rdb.Client, check, task)
	raw, err := rdb.Client.Get(ctx, key).Result()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &status))
	assert.Equal(t, checks.StatusSuccess, status.StatusText)
}
