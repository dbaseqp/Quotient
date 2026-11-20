package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/require"
)

// A round that runs to its deadline (a runner lost a result) ends after the next round was due. The
// next round must still wait roundFinishSettle after it, or it starts while Divisor rotates the
// runners' source addresses on round_finish and its first checks time out against healthy services.
func TestStart_OverrunRoundIsFollowedBySettleGap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	se := NewTestEngine(t, redis, pg, 3)

	var mu sync.Mutex
	var starts, ends []time.Time
	second := make(chan struct{})
	se.roundFunc = func() error {
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, time.Now())
		switch len(starts) {
		case 1:
			// Collection ran past the next round's start, as with a missing result.
			se.NextRoundStartTime = time.Now().Add(-2 * time.Second)
		case 2:
			close(second)
		}
		ends = append(ends, time.Now())
		return nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		se.Start()
	}()

	select {
	case <-second:
	case <-time.After(roundFinishSettle + 15*time.Second):
		t.Fatal("second round never ran")
	}
	require.NoError(t, se.ResetScores())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after ResetScores")
	}

	mu.Lock()
	defer mu.Unlock()
	gap := starts[1].Sub(ends[0])
	require.GreaterOrEqual(t, gap, roundFinishSettle-50*time.Millisecond,
		"round after an overrun started %s after round_finish; Divisor needs %s to rotate", gap, roundFinishSettle)
}
