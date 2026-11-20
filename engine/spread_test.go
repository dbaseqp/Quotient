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

// TestPropertySpreadDelayFitsBudget: whatever the draw, the held-back work
// still has its whole worst case within the budget, and never waits longer
// than the window.
func TestPropertySpreadDelayFitsBudget(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		budget := time.Duration(rapid.Int64Range(-int64(10*time.Second), int64(200*time.Second)).Draw(t, "budget"))
		timeout := rapid.IntRange(1, 60).Draw(t, "timeout")
		attempts := rapid.IntRange(1, 4).Draw(t, "attempts")
		attemptDelay := rapid.IntRange(0, 60).Draw(t, "attemptDelay")
		window := time.Duration(rapid.Int64Range(0, int64(200*time.Second)).Draw(t, "window"))
		extreme := rapid.Bool().Draw(t, "extremeDraw")
		draw := rand.Int64N
		if extreme {
			draw = maxDraw
		}

		d := SpreadDelay(budget, timeout, attempts, attemptDelay, window, draw)
		worst := time.Duration(checks.WorstCaseSeconds(timeout, attempts, attemptDelay)) * time.Second

		require.GreaterOrEqual(t, d, time.Duration(0))
		require.LessOrEqual(t, d, window)
		if d > 0 {
			require.LessOrEqual(t, d+worst, budget, "delay %s + worst case %s overruns the budget", d, worst)
		}
		if extreme {
			require.Equal(t, max(min(window, budget-worst), 0), d, "the largest draw reaches the bound exactly")
		}
	})
}

func TestSpreadDelayOffOrNoSlack(t *testing.T) {
	never := func(int64) int64 { t.Fatal("draw called without slack"); return 0 }
	assert.Zero(t, SpreadDelay(time.Hour, 30, 2, 45, 0, never), "window 0 turns spreading off")
	assert.Zero(t, SpreadDelay(105*time.Second, 30, 2, 45, time.Hour, never), "105 s worst case fills the budget")
	assert.Zero(t, SpreadDelay(-time.Second, 30, 1, 0, time.Hour, never), "budget spent")
}

// TestSpreadDelayInv2Settings pins the slack Invitational #2's settings leave
// (Delay 120, Jitter 10, Timeout 30, Attempts 2, AttemptDelay 45): the
// 110 s shortest round minus the 105 s worst case and SpreadMargin.
func TestSpreadDelayInv2Settings(t *testing.T) {
	budget := 110*time.Second - SpreadMargin
	assert.Zero(t, SpreadDelay(budget, 30, 2, 45, 110*time.Second, maxDraw), "no room once the round tail is kept free")
	// Timeout 15 and AttemptDelay 20 free most of a minute.
	assert.Equal(t, 52*time.Second, SpreadDelay(budget, 15, 2, 20, 110*time.Second, maxDraw))
}

func TestSpreadDelayIsSpread(t *testing.T) {
	var lo, hi int
	for range 1000 {
		d := SpreadDelay(70*time.Second, 10, 1, 0, 60*time.Second, rand.Int64N)
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

// TestSpreadDelayInv3Settings draws a whole event's start offsets with
// Invitational #3's settings (Delay 120, Jitter 10, Timeout 30, Attempts 1,
// AttemptDelay 0, Spread omitted = 110) for 50 teams and 15 services. Every
// check must start inside the 72 s window (110 s shortest round - 30 s -
// SpreadMargin), the window must be used end to end, and neither the order
// teams are reached in nor a service's offset may carry over between rounds.
func TestSpreadDelayInv3Settings(t *testing.T) {
	const teams, services, rounds = 50, 15, 40
	window := 72 * time.Second
	draw := rand.New(rand.NewPCG(3, 2027)).Int64N
	off := make([][][]time.Duration, rounds) // [round][team][service]
	for r := range rounds {
		off[r] = make([][]time.Duration, teams)
		for tm := range teams {
			off[r][tm] = make([]time.Duration, services)
			for s := range services {
				d := SpreadDelay(110*time.Second-SpreadMargin, 30, 1, 0, 110*time.Second, draw)
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
		assert.Greater(t, hi, 70*time.Second, "round %d: the window's end is used", r)
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
