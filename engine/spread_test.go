package engine

import (
	"math"
	"math/rand/v2"
	"sort"
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

// TestSpreadStartInv3Settings draws a whole event's start offsets with
// Invitational #3's settings (Delay 120, Jitter 10, Timeout 30, Attempts 1,
// AttemptDelay 0, Spread omitted = 110) for 50 teams and 15 services. Every
// check must start inside the 78 s window (110 s shortest round - 30 s - 2 s
// margin), the window must be used end to end, and neither the order teams
// are reached in nor a service's offset may carry over between rounds.
func TestSpreadStartInv3Settings(t *testing.T) {
	const teams, services, rounds = 50, 15, 40
	window := 78 * time.Second
	draw := rand.New(rand.NewPCG(3, 2027)).Int64N
	off := make([][][]time.Duration, rounds) // [round][team][service]
	for r := range rounds {
		start := time.Unix(1_800_000_000, 0).Add(time.Duration(r) * 2 * time.Minute)
		off[r] = make([][]time.Duration, teams)
		for tm := range teams {
			off[r][tm] = make([]time.Duration, services)
			for s := range services {
				at := SpreadStart(start, 110*time.Second, 30, 1, 0, 110*time.Second, draw)
				d := at.Sub(start)
				require.GreaterOrEqual(t, d, time.Duration(0))
				require.LessOrEqual(t, d, window, "a check started at %s would not finish in the shortest round", d)
				off[r][tm][s] = d
			}
		}
	}

	firstTeams := map[int]bool{}
	var rhoSum float64
	for r := range rounds {
		lo, hi := window, time.Duration(0)
		first := make([]float64, teams) // when each team is first probed
		for tm := range teams {
			first[tm] = float64(window)
			for _, d := range off[r][tm] {
				lo, hi = min(lo, d), max(hi, d)
				first[tm] = min(first[tm], float64(d))
			}
		}
		assert.Less(t, lo, 2*time.Second, "round %d: the window's start is used", r)
		assert.Greater(t, hi, 76*time.Second, "round %d: the window's end is used", r)
		rho := spearman(teamIndexes(teams), first)
		assert.Less(t, abs(rho), 0.4, "round %d: teams reached in team order (rho %.2f)", r, rho)
		rhoSum += rho
		firstTeams[argmin(first)] = true
	}
	assert.Less(t, abs(rhoSum/rounds), 0.1, "on average teams are reached in team order")
	assert.GreaterOrEqual(t, len(firstTeams), 15, "the first team reached should change from round to round")

	// A service's offset in one round says nothing about the next.
	var a, b []float64
	for r := 1; r < rounds; r++ {
		for tm := range teams {
			for s := range services {
				a = append(a, float64(off[r-1][tm][s]))
				b = append(b, float64(off[r][tm][s]))
			}
		}
	}
	assert.Less(t, abs(pearson(a, b)), 0.05, "offsets repeat across rounds")
}

func TestStartDelay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	task := &Task{Attempts: 1, Deadline: now.Add(110 * time.Second)}
	assert.Zero(t, StartDelay(now, task, 30), "no StartAt: start at once")

	task.StartAt = now.Add(40 * time.Second)
	assert.Equal(t, 40*time.Second, StartDelay(now, task, 30))
	assert.Zero(t, StartDelay(now.Add(50*time.Second), task, 30), "picked up after StartAt: start at once")

	// A StartAt the deadline can't absorb (deadline moved, clock skew) is
	// pulled in so the 30 s attempt and the margin still fit.
	task.StartAt = now.Add(100 * time.Second)
	assert.Equal(t, 78*time.Second, StartDelay(now, task, 30))
}

func teamIndexes(n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = float64(i)
	}
	return v
}

func argmin(v []float64) int {
	m := 0
	for i := range v {
		if v[i] < v[m] {
			m = i
		}
	}
	return m
}

func abs(x float64) float64 { return max(x, -x) }

func ranks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	r := make([]float64, len(v))
	for rank, i := range idx {
		r[i] = float64(rank)
	}
	return r
}

func spearman(x, y []float64) float64 { return pearson(ranks(x), ranks(y)) }

func pearson(x, y []float64) float64 {
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	var sxy, sxx, syy float64
	for i := range x {
		sxy += (x[i] - mx) * (y[i] - my)
		sxx += (x[i] - mx) * (x[i] - mx)
		syy += (y[i] - my) * (y[i] - my)
	}
	return sxy / math.Sqrt(sxx*syy)
}
