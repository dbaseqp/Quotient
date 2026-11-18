package engine

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitScoresRefreshed waits for the engine's async view refresh, then refreshes
// synchronously so the view covers every stored round.
func waitScoresRefreshed(t *testing.T, se *ScoringEngine) {
	t.Helper()
	require.Eventually(t, func() bool { return !se.Refreshing.Load() }, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, se.DB.RefreshScoresMaterializedView())
}

func processRound(se *ScoringEngine, round uint, results ...checks.Result) {
	se.CurrentRound = round
	for i := range results {
		results[i].RoundID = round
	}
	se.processCollectedResults(results)
}

func exportTotal(t *testing.T, se *ScoringEngine, team uint) int {
	t.Helper()
	scores, err := se.DB.GetServiceScores()
	require.NoError(t, err)
	total := 0
	for _, s := range scores {
		if s.TeamID == team {
			total += s.Points - s.TotalPenalty
		}
	}
	return total
}

// A round with no checks for a team (lost round, all of its checks disabled)
// must carry the team's total forward instead of showing only its penalties.
func TestScoreGraphCarriesTotalsThroughRoundsWithoutChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	a := pg.CreateTestTeam(t, "GraphGapA", "01")
	b := pg.CreateTestTeam(t, "GraphGapB", "02")

	se := NewTestEngine(t, redis, pg, 2) // threshold 2, penalty 30
	se.CurrentRoundStartTime = time.Now()

	pass := func(team uint) checks.Result {
		return checks.Result{TeamID: team, ServiceName: "svc", Status: true, Points: 10}
	}
	fail := func(team uint) checks.Result {
		return checks.Result{TeamID: team, ServiceName: "svc", Status: false, Points: 10}
	}
	for r := uint(1); r <= 5; r++ {
		processRound(se, r, pass(a.ID), pass(b.ID))
		waitScoresRefreshed(t, se)
	}
	processRound(se, 6, fail(a.ID), pass(b.ID))
	processRound(se, 7, fail(a.ID), pass(b.ID)) // A: SLA (30) in round 7
	waitScoresRefreshed(t, se)
	processRound(se, 8) // lost round: no results
	processRound(se, 9, pass(a.ID), pass(b.ID))
	processRound(se, 10, pass(b.ID)) // all of A's checks disabled
	waitScoresRefreshed(t, se)

	scores, err := pg.DB.GetServiceCheckSumByRound()
	require.NoError(t, err)
	require.Len(t, scores, 10)

	wantA := []int{10, 20, 30, 40, 50, 50, 20, 20, 30, 30}
	wantB := []int{10, 20, 30, 40, 50, 60, 70, 70, 80, 90}
	for i := range scores {
		assert.Equal(t, wantA[i], scores[i][a.ID], "A after round %d", i+1)
		assert.Equal(t, wantB[i], scores[i][b.ID], "B after round %d", i+1)
	}
	assert.Equal(t, exportTotal(t, se, a.ID), scores[9][a.ID], "graph's last point matches the score export")
	assert.Equal(t, exportTotal(t, se, b.ID), scores[9][b.ID], "graph's last point matches the score export")
}

// A round stored before the view refresh (refresh pending or skipped because
// one is in flight) must not pair the new round's SLAs with the previous
// round's points.
func TestScoreGraphTotalsBeforeViewRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	a := pg.CreateTestTeam(t, "GraphStaleA", "01")
	b := pg.CreateTestTeam(t, "GraphStaleB", "02")

	se := NewTestEngine(t, redis, pg, 2) // threshold 2, penalty 30
	se.CurrentRoundStartTime = time.Now()

	res := func(team uint, svc string, ok bool) checks.Result {
		return checks.Result{TeamID: team, ServiceName: svc, Status: ok, Points: 10}
	}
	// A has two services, B one. A's svc2 fails rounds 6-8: SLA (30) in round 7.
	for r := uint(1); r <= 7; r++ {
		processRound(se, r, res(a.ID, "svc1", true), res(a.ID, "svc2", r < 6), res(b.ID, "svc1", true))
		waitScoresRefreshed(t, se)
	}
	// A = 70 + 50 - 30 = 90, B = 70.

	se.Refreshing.Store(true) // a refresh is still in flight
	processRound(se, 8, res(a.ID, "svc1", true), res(a.ID, "svc2", false), res(b.ID, "svc1", true))
	processRound(se, 9, res(a.ID, "svc1", true), res(a.ID, "svc2", false), res(b.ID, "svc1", true))
	se.Refreshing.Store(false)
	// Round 9 adds a second SLA for A that the view does not cover yet.

	scores, err := pg.DB.GetServiceCheckSumByRound()
	require.NoError(t, err)
	assert.Len(t, scores, 7, "the graph ends at the last round the view covers")
	assert.Equal(t, 90, scores[6][a.ID])
	assert.Equal(t, 70, scores[6][b.ID])

	require.NoError(t, se.DB.RefreshScoresMaterializedView())
	scores, err = pg.DB.GetServiceCheckSumByRound()
	require.NoError(t, err)
	require.Len(t, scores, 9)
	assert.Equal(t, 90+10, scores[7][a.ID])
	assert.Equal(t, 90+20-30, scores[8][a.ID])
	assert.Equal(t, 90, scores[8][b.ID])
	assert.Equal(t, exportTotal(t, se, a.ID), scores[8][a.ID], "graph's last point matches the score export")
}
