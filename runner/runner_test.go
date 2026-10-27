package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
