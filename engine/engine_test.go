package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"quotient/engine/checks"
	"quotient/engine/config"
	"quotient/engine/db"
	"quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestEngine creates a minimal engine for testing
func newTestEngine(t *testing.T, redis *testutil.RedisContainer, slaThreshold int) *ScoringEngine {
	t.Helper()

	conf := &config.ConfigSettings{
		RequiredSettings: config.RequiredConfig{
			EventName:    "Test Event",
			EventType:    "rvb",
			DBConnectURL: "test", // Already connected via db.Connect
			BindAddress:  "127.0.0.1",
		},
		MiscSettings: config.MiscConfig{
			Delay:        5,
			Jitter:       0,
			Timeout:      3,
			Points:       10,
			SlaThreshold: slaThreshold,
			SlaPenalty:   30,
		},
	}

	return &ScoringEngine{
		Config:           conf,
		CredentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		RedisClient:      redis.Client,
		CurrentRound:     1,
	}
}

func TestProcessCollectedResults_SavesRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	// Clean slate
	redis.Client.FlushDB(context.Background())
	db.ResetScores()

	// Create team
	team := db.TeamSchema{ID: 1, Name: "Team 1", Active: true}
	_, err := db.CreateTeam(team)
	require.NoError(t, err)

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRound = 1
	engine.CurrentRoundStartTime = time.Now()

	// Process a passing result
	results := []checks.Result{
		{
			TeamID:      1,
			ServiceName: "web-service",
			ServiceType: "Web",
			RoundID:     1,
			Status:      true,
			Points:      10,
		},
	}

	engine.processCollectedResults(results)

	// Verify round was saved
	round, err := db.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID)
	require.Len(t, round.Checks, 1)
	assert.Equal(t, "web-service", round.Checks[0].ServiceName)
	assert.True(t, round.Checks[0].Result)
	assert.Equal(t, 10, round.Checks[0].Points)
}

func TestProcessCollectedResults_TracksUptime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	redis.Client.FlushDB(context.Background())
	db.ResetScores()

	team := db.TeamSchema{ID: 1, Name: "Team 1", Active: true}
	db.CreateTeam(team)

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Round 1: pass
	engine.CurrentRound = 1
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: true, Points: 10, RoundID: 1},
	})

	// Round 2: fail
	engine.CurrentRound = 2
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: false, Points: 0, RoundID: 2},
	})

	// Round 3: pass
	engine.CurrentRound = 3
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: true, Points: 10, RoundID: 3},
	})

	// Verify uptime: 2 passed out of 3 total
	uptime := engine.UptimePerService[1]["svc"]
	assert.Equal(t, 2, uptime.PassedChecks)
	assert.Equal(t, 3, uptime.TotalChecks)
}

func TestProcessCollectedResults_TriggersSLA(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	redis.Client.FlushDB(context.Background())
	db.ResetScores()

	team := db.TeamSchema{ID: 1, Name: "Team 1", Active: true}
	db.CreateTeam(team)

	// SLA threshold of 3 consecutive failures
	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Fail 3 times in a row - should trigger SLA
	for round := uint(1); round <= 3; round++ {
		engine.CurrentRound = round
		engine.processCollectedResults([]checks.Result{
			{TeamID: 1, ServiceName: "failing-svc", Status: false, Points: 0, RoundID: round},
		})
	}

	// Verify SLA was created
	slas, err := db.GetSLAs()
	require.NoError(t, err)
	require.Len(t, slas, 1, "expected 1 SLA violation")
	assert.Equal(t, uint(1), slas[0].TeamID)
	assert.Equal(t, "failing-svc", slas[0].ServiceName)
	assert.Equal(t, uint(3), slas[0].RoundID)
	assert.Equal(t, 30, slas[0].Penalty)

	// SLA counter should reset after triggering
	assert.Equal(t, 0, engine.SlaPerService[1]["failing-svc"])
}

func TestProcessCollectedResults_SLAResetsOnPass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	redis.Client.FlushDB(context.Background())
	db.ResetScores()

	team := db.TeamSchema{ID: 1, Name: "Team 1", Active: true}
	db.CreateTeam(team)

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Fail twice
	engine.CurrentRound = 1
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: false, RoundID: 1},
	})
	engine.CurrentRound = 2
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: false, RoundID: 2},
	})

	// Pass once - should reset counter
	engine.CurrentRound = 3
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: true, Points: 10, RoundID: 3},
	})

	// Counter should be 0
	assert.Equal(t, 0, engine.SlaPerService[1]["svc"])

	// Fail twice more - still not at threshold
	engine.CurrentRound = 4
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: false, RoundID: 4},
	})
	engine.CurrentRound = 5
	engine.processCollectedResults([]checks.Result{
		{TeamID: 1, ServiceName: "svc", Status: false, RoundID: 5},
	})

	// No SLA should be created (only 2 consecutive fails after the pass)
	slas, err := db.GetSLAs()
	require.NoError(t, err)
	assert.Len(t, slas, 0, "expected no SLA violations")
}

func TestProcessCollectedResults_MultipleTeamsIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	redis.Client.FlushDB(context.Background())
	db.ResetScores()

	// Create two teams
	db.CreateTeam(db.TeamSchema{ID: 1, Name: "Team 1", Active: true})
	db.CreateTeam(db.TeamSchema{ID: 2, Name: "Team 2", Active: true})

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Team 1 fails, Team 2 passes - over 3 rounds
	for round := uint(1); round <= 3; round++ {
		engine.CurrentRound = round
		engine.processCollectedResults([]checks.Result{
			{TeamID: 1, ServiceName: "svc", Status: false, RoundID: round},
			{TeamID: 2, ServiceName: "svc", Status: true, Points: 10, RoundID: round},
		})
	}

	// Only Team 1 should have SLA
	slas, err := db.GetSLAs()
	require.NoError(t, err)
	require.Len(t, slas, 1)
	assert.Equal(t, uint(1), slas[0].TeamID)

	// Team 2 should have 100% uptime
	assert.Equal(t, 3, engine.UptimePerService[2]["svc"].PassedChecks)
	assert.Equal(t, 3, engine.UptimePerService[2]["svc"].TotalChecks)
}
