package engine

import (
	"context"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSlaOverrideEngine returns an engine with global SLA rules 5/30 whose
// config holds box01-strict, which sets its own 2/7, and box01-default, which
// sets none.
func newSlaOverrideEngine(t *testing.T) (*ScoringEngine, *testutil.PostgresContainer, uint) {
	t.Helper()
	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	team := pg.CreateTestTeam(t, "Team SLA Override", "01")

	se := NewTestEngine(t, redis, pg, 5)
	se.CurrentRoundStartTime = time.Now()
	conf := se.Config.Current()
	require.Equal(t, 30, *conf.MiscSettings.SlaPenalty)

	strictPenalty := 7
	strict := &checks.Tcp{Service: checks.Service{Display: "strict", Port: 22, SlaThreshold: 2, SlaPenalty: &strictPenalty}}
	plain := &checks.Tcp{Service: checks.Service{Display: "default", Port: 22}}
	runners := []checks.Runner{strict, plain}
	for _, r := range runners {
		require.NoError(t, r.Verify("box01", "192.0.2.1", conf.MiscSettings.Points, conf.MiscSettings.Timeout,
			*conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold))
	}
	conf.Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: runners}}
	return se, pg, team.ID
}

func failBothChecks(se *ScoringEngine, teamID uint, rounds int) {
	for range rounds {
		se.processCollectedResults(se.Config.Current(), []checks.Result{
			{TeamID: teamID, ServiceName: "box01-strict", Status: false, RoundID: se.CurrentRound},
			{TeamID: teamID, ServiceName: "box01-default", Status: false, RoundID: se.CurrentRound},
		})
		se.CurrentRound++
	}
}

// A check's own SlaThreshold and SlaPenalty replace MiscSettings' for that
// check only.
func TestProcessCollectedResults_PerCheckSlaOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	se, pg, teamID := newSlaOverrideEngine(t)

	failBothChecks(se, teamID, 2)
	slas := slasByService(t, pg, teamID)
	assert.Equal(t, [2]int{1, 7}, slas["box01-strict"], "2 failures cost box01-strict one SLA of 7")
	assert.Equal(t, [2]int{0, 0}, slas["box01-default"], "box01-default needs 5 failures")

	failBothChecks(se, teamID, 3)
	slas = slasByService(t, pg, teamID)
	assert.Equal(t, [2]int{2, 14}, slas["box01-strict"])
	assert.Equal(t, [2]int{1, 30}, slas["box01-default"], "box01-default follows the global 5/30")
	assert.Equal(t, 1, se.SlaPerService[teamID]["box01-strict"])
	assert.Equal(t, 0, se.SlaPerService[teamID]["box01-default"])
}

// A restart rebuilds each check's run of failures with that check's threshold,
// so it resumes where the engine left off.
func TestLoadSLAs_PerCheckThresholds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	se, pg, teamID := newSlaOverrideEngine(t)

	failBothChecks(se, teamID, 3)
	require.Equal(t, 1, se.SlaPerService[teamID]["box01-strict"])
	require.Equal(t, 3, se.SlaPerService[teamID]["box01-default"])

	rules := se.Config.Current().SlaRules()
	restored := make(map[uint]map[string]int)
	require.NoError(t, pg.DB.LoadSLAs(&restored, func(serviceName string) int { return rules(serviceName).Threshold }))
	assert.Equal(t, se.SlaPerService[teamID], restored[teamID])
}

// slasByService returns each service's SLA count and total penalty for team.
func slasByService(t *testing.T, pg *testutil.PostgresContainer, teamID uint) map[string][2]int {
	t.Helper()
	scores, err := pg.DB.GetServiceScores()
	require.NoError(t, err)
	out := make(map[string][2]int)
	for _, s := range scores {
		if s.TeamID == teamID {
			out[s.ServiceName] = [2]int{s.Violations, s.TotalPenalty}
		}
	}
	return out
}
