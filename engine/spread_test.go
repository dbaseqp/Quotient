package engine

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func maxDraw(n int64) int64 { return n - 1 }

// TestPropertySpreadDelayFitsRound: whatever the draw, the held-back work
// still has its whole worst case plus SpreadMargin before the deadline, and
// never waits longer than the window.
func TestPropertySpreadDelayFitsRound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		now := time.Unix(1_800_000_000, 0)
		deadline := now.Add(time.Duration(rapid.Int64Range(0, int64(200*time.Second)).Draw(t, "untilDeadline")))
		timeout := rapid.IntRange(1, 60).Draw(t, "timeout")
		attempts := rapid.IntRange(1, 4).Draw(t, "attempts")
		attemptDelay := rapid.IntRange(0, 60).Draw(t, "attemptDelay")
		window := time.Duration(rapid.Int64Range(0, int64(200*time.Second)).Draw(t, "window"))
		extreme := rapid.Bool().Draw(t, "extremeDraw")
		draw := rand.Int64N
		if extreme {
			draw = maxDraw
		}

		d := SpreadDelay(now, deadline, timeout, attempts, attemptDelay, window, draw)
		worst := time.Duration(checks.WorstCaseSeconds(timeout, attempts, attemptDelay)) * time.Second

		require.GreaterOrEqual(t, d, time.Duration(0))
		require.LessOrEqual(t, d, max(window, 0))
		if d > 0 {
			require.False(t, now.Add(d+worst+SpreadMargin).After(deadline),
				"delay %s + worst case %s + margin overruns the deadline %s away", d, worst, deadline.Sub(now))
		}
		if extreme {
			limit := min(window, deadline.Sub(now)-worst-SpreadMargin)
			require.Equal(t, max(limit, 0), d, "the largest draw reaches the bound exactly")
		}
	})
}

func TestSpreadDelayOffOrNoSlack(t *testing.T) {
	now := time.Now()
	never := func(int64) int64 { t.Fatal("draw called without slack"); return 0 }
	assert.Zero(t, SpreadDelay(now, now.Add(time.Hour), 30, 2, 45, 0, never), "window 0 turns spreading off")
	assert.Zero(t, SpreadDelay(now, now.Add(106*time.Second), 30, 2, 45, time.Hour, never), "105 s worst case + 2 s margin > 106 s")
	assert.Zero(t, SpreadDelay(now, now.Add(-time.Second), 30, 1, 0, time.Hour, never), "deadline passed")
}

// TestSpreadDelayInv2Settings pins the slack Invitational #2's settings leave
// (Delay 120, Jitter 10, Timeout 30, Attempts 2, AttemptDelay 45): a round of
// 110-130 s minus the 105 s worst case and the 2 s margin.
func TestSpreadDelayInv2Settings(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		round, want time.Duration
	}{
		{110 * time.Second, 3 * time.Second},
		{120 * time.Second, 13 * time.Second},
		{130 * time.Second, 23 * time.Second},
	} {
		assert.Equal(t, tc.want, SpreadDelay(now, now.Add(tc.round), 30, 2, 45, 110*time.Second, maxDraw), "round %s", tc.round)
	}
	// Timeout 15 and AttemptDelay 20 free a minute even in the shortest round.
	assert.Equal(t, 58*time.Second, SpreadDelay(now, now.Add(110*time.Second), 15, 2, 20, 110*time.Second, maxDraw))
}

func TestSpreadDelayIsSpread(t *testing.T) {
	now := time.Now()
	var lo, hi int
	for range 1000 {
		d := SpreadDelay(now, now.Add(100*time.Second), 10, 1, 0, 60*time.Second, rand.Int64N)
		if d < 20*time.Second {
			lo++
		}
		if d > 40*time.Second {
			hi++
		}
	}
	// Uniform over [0, 60 s]: about a third in each end; 200 is far in the tail.
	assert.Greater(t, lo, 200)
	assert.Greater(t, hi, 200)
}
