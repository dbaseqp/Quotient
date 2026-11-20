package engine

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failNextInsert makes the next INSERT into table fail once, like a transient
// Postgres error.
func failNextInsert(t *testing.T, pg *testutil.PostgresContainer, table string) {
	t.Helper()
	seq, fn, trg := "fail_once_"+table+"_seq", "fail_once_"+table, "fail_once_"+table+"_trg"
	pg.Exec(t, "CREATE SEQUENCE "+seq)
	pg.Exec(t, `CREATE FUNCTION `+fn+`() RETURNS trigger AS $$
BEGIN
  IF nextval('`+seq+`') = 1 THEN
    RAISE EXCEPTION 'simulated transient failure on `+table+`';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`)
	pg.Exec(t, "CREATE TRIGGER "+trg+" BEFORE INSERT ON "+table+" FOR EACH ROW EXECUTE FUNCTION "+fn+"()")
	t.Cleanup(func() {
		pg.Exec(t, "DROP TRIGGER IF EXISTS "+trg+" ON "+table)
		pg.Exec(t, "DROP FUNCTION IF EXISTS "+fn+"()")
		pg.Exec(t, "DROP SEQUENCE IF EXISTS "+seq)
	})
}

func roundWriteEngine(t *testing.T, slaThreshold int) (*ScoringEngine, *testutil.PostgresContainer, db.TeamSchema) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.Exec(t, "UPDATE team_schemas SET active = false")
	team := pg.CreateTestTeam(t, "Round Write Team", "01")
	se := NewTestEngine(t, redis, pg, slaThreshold)
	se.Config.Current().MiscSettings.Delay = 3
	svc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: []checks.Runner{svc}}}
	fakeRunner(t, redis, func(task Task) []checks.Result {
		res := passResult(task)
		res[0].Status = false
		return res
	})
	return se, pg, team
}

// A failed round write is returned without advancing CurrentRound, so the
// round loop's retry stores the round under the same ID instead of skipping it.
func TestRvb_RoundWriteFailureIsRetriedUnderSameID(t *testing.T) {
	se, pg, team := roundWriteEngine(t, 3)
	failNextInsert(t, pg, "round_schemas")

	require.Error(t, se.rvb())
	assert.Equal(t, uint(1), se.CurrentRound)
	assert.Empty(t, se.UptimePerService[team.ID], "uptime only changes once the round is stored")

	require.NoError(t, se.rvb())
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID)
	assert.Equal(t, 1, se.UptimePerService[team.ID]["box01-web"].TotalChecks)
}

// An SLA violation is written with its round: if the SLA insert fails the
// round is rolled back and retried, so the penalty is never lost.
func TestRvb_SLAWriteFailureKeepsPenalty(t *testing.T) {
	se, pg, team := roundWriteEngine(t, 2)
	failNextInsert(t, pg, "sla_schemas")

	require.NoError(t, se.rvb()) // round 1: first failure
	require.Error(t, se.rvb())   // round 2: SLA insert fails, round rolled back
	assert.Equal(t, uint(2), se.CurrentRound)
	assert.Equal(t, 1, se.SlaPerService[team.ID]["box01-web"], "the streak is unchanged by the failed write")
	require.NoError(t, se.rvb()) // round 2 again

	_, slas, _, err := pg.DB.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, slas, "two consecutive failures at threshold 2 leave one SLA")
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(2), round.ID)
}

// A round ID that is already stored (a commit whose reply was lost, or a
// counter that failed to load at startup) is not retried forever: the failed
// write reloads the counter from the database, announces the stored round,
// and the next round follows it.
func TestRvb_StoredRoundIDReloadsCounter(t *testing.T) {
	se, pg, team := roundWriteEngine(t, 3)

	require.NoError(t, se.rvb())
	roundCh, unsubscribe := se.SubscribeRounds()
	defer unsubscribe()
	se.CurrentRound = 1
	require.Error(t, se.rvb())
	assert.Equal(t, uint(2), se.CurrentRound)
	assert.Equal(t, 1, se.UptimePerService[team.ID]["box01-web"].TotalChecks, "the rejected round is not counted")
	select {
	case <-roundCh:
	default:
		t.Fatal("the stored round was not announced")
	}

	require.NoError(t, se.rvb())
	round, err := pg.DB.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(2), round.ID)
}

// LoadSLAs rebuilds streaks at startup from the failures after each service's
// last pass or stored violation, whatever the threshold or the physical row
// order.
func TestLoadSLAsCountsFailuresSinceLastReset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	_, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	team := pg.CreateTestTeam(t, "SLA Streak Team", "01")

	for _, rc := range []struct {
		id  uint
		web bool
	}{{1, true}, {2, false}, {3, false}} {
		round := db.RoundSchema{ID: rc.id, StartTime: time.Now(), Checks: []db.ServiceCheckSchema{
			{TeamID: team.ID, ServiceName: "box01-web", Points: 10, Result: rc.web},
			{TeamID: team.ID, ServiceName: "box01-dns", Points: 10, Result: false},
		}}
		if rc.id == 2 {
			round.SLAs = []db.SLASchema{{TeamID: team.ID, RoundID: 2, ServiceName: "box01-dns", Penalty: 5}}
		}
		_, err := pg.DB.CreateRound(round)
		require.NoError(t, err)
	}
	// Updating an old row moves its tuple to the end of the heap.
	pg.Exec(t, "UPDATE service_check_schemas SET debug = debug WHERE round_id = 1")

	sla, err := pg.DB.LoadSLAs()
	require.NoError(t, err)
	assert.Equal(t, 2, sla[team.ID]["box01-web"], "rounds pass, fail, fail leave a streak of 2")
	assert.Equal(t, 1, sla[team.ID]["box01-dns"], "the violation in round 2 resets the streak")
}
