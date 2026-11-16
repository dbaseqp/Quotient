package engine

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStart_RetriesOnNonResetError verifies that a transient error from a
// round does not stop the engine loop; subsequent rounds still execute.
func TestStart_RetriesOnNonResetError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	se := NewTestEngine(t, redis, pg, 3)

	var roundCalls atomic.Int32
	secondRoundDone := make(chan struct{})

	se.roundFunc = func() error {
		n := roundCalls.Add(1)
		if n == 1 {
			return errors.New("transient db error")
		}
		// Signal that the second round ran successfully.
		select {
		case <-secondRoundDone:
		default:
			close(secondRoundDone)
		}
		// Return errReset to exit the loop cleanly after the second round.
		return errReset
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		se.Start()
	}()

	select {
	case <-secondRoundDone:
		// Good: second round ran, loop retried after transient error.
	case <-time.After(15 * time.Second):
		t.Fatal("second round never ran; engine loop did not retry after non-reset error")
	}

	// Second round signalled; now trigger a reset so Start() can return.
	require.NoError(t, se.ResetScores())
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after ResetScores")
	}

	assert.GreaterOrEqual(t, int(roundCalls.Load()), 2, "expected at least 2 round calls")
}

// TestStart_ResetErrorStopsLoop verifies that errReset still exits the loop.
func TestStart_ResetErrorStopsLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	se := NewTestEngine(t, redis, pg, 3)

	var roundCalls atomic.Int32

	se.roundFunc = func() error {
		roundCalls.Add(1)
		return errReset
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		se.Start()
	}()

	// Give the first round time to fire and exit the goroutine.
	time.Sleep(300 * time.Millisecond)

	// The goroutine should have returned; now trigger the reset so Start()
	// can unblock from waitForReset.
	require.NoError(t, se.ResetScores())
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after reset")
	}

	assert.Equal(t, int32(1), roundCalls.Load(), "loop should have stopped after the first errReset")
}
