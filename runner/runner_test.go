package main

import (
	"context"
	"encoding/json"
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
