package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quotient/engine/checks"
	"quotient/engine/config"
	"quotient/engine/db"
	"quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTeamCounter provides unique team IDs across test runs
var testTeamCounter atomic.Uint64

func init() {
	testTeamCounter.Store(uint64(time.Now().UnixNano() % 1_000_000))
}

// nextTeamID returns a unique team ID for testing
func nextTeamID() uint {
	return uint(testTeamCounter.Add(1))
}

// createTestTeam creates a team with a unique ID, or returns existing if name matches
func createTestTeam(t *testing.T, name string, identifier string) db.TeamSchema {
	t.Helper()
	teamID := nextTeamID()
	team := db.TeamSchema{
		ID:         teamID,
		Name:       fmt.Sprintf("%s-%d", name, teamID),
		Identifier: identifier,
		Active:     true,
	}
	_, err := db.CreateTeam(team)
	require.NoError(t, err)
	return team
}

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
			Jitter:       1, // Must be non-zero to avoid rand.Intn(0) panic
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

	team := createTestTeam(t, "Team", "01")

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRound = 1
	engine.CurrentRoundStartTime = time.Now()

	// Process a passing result
	results := []checks.Result{
		{
			TeamID:      team.ID,
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

	// Find our check in the round (other tests may have created checks too)
	var foundCheck *db.ServiceCheckSchema
	for i := range round.Checks {
		if round.Checks[i].TeamID == team.ID {
			foundCheck = &round.Checks[i]
			break
		}
	}
	require.NotNil(t, foundCheck, "should find check for our team")
	assert.Equal(t, "web-service", foundCheck.ServiceName)
	assert.True(t, foundCheck.Result)
	assert.Equal(t, 10, foundCheck.Points)
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

	team := createTestTeam(t, "Team", "01")

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Round 1: pass
	engine.CurrentRound = 1
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: true, Points: 10, RoundID: 1},
	})

	// Round 2: fail
	engine.CurrentRound = 2
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: false, Points: 0, RoundID: 2},
	})

	// Round 3: pass
	engine.CurrentRound = 3
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: true, Points: 10, RoundID: 3},
	})

	// Verify uptime: 2 passed out of 3 total
	uptime := engine.UptimePerService[team.ID]["svc"]
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

	team := createTestTeam(t, "Team SLA", "01")

	// SLA threshold of 3 consecutive failures
	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Fail 3 times in a row - should trigger SLA
	for round := uint(1); round <= 3; round++ {
		engine.CurrentRound = round
		engine.processCollectedResults([]checks.Result{
			{TeamID: team.ID, ServiceName: "failing-svc", Status: false, Points: 0, RoundID: round},
		})
	}

	// SLA counter should reset after triggering (this proves SLA was created)
	assert.Equal(t, 0, engine.SlaPerService[team.ID]["failing-svc"],
		"SLA counter should reset to 0 after SLA is triggered")

	// Verify via team score which includes SLA penalties
	_, slaCount, _, err := db.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, slaCount, "expected 1 SLA violation")
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

	team := createTestTeam(t, "Team SLA Reset", "01")

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Fail twice
	engine.CurrentRound = 1
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: false, RoundID: 1},
	})
	engine.CurrentRound = 2
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: false, RoundID: 2},
	})

	// Pass once - should reset counter
	engine.CurrentRound = 3
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: true, Points: 10, RoundID: 3},
	})

	// Counter should be 0
	assert.Equal(t, 0, engine.SlaPerService[team.ID]["svc"])

	// Fail twice more - still not at threshold
	engine.CurrentRound = 4
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: false, RoundID: 4},
	})
	engine.CurrentRound = 5
	engine.processCollectedResults([]checks.Result{
		{TeamID: team.ID, ServiceName: "svc", Status: false, RoundID: 5},
	})

	// Counter should be 2 (not triggered yet)
	assert.Equal(t, 2, engine.SlaPerService[team.ID]["svc"],
		"SLA counter should be 2 after 2 consecutive failures")

	// Verify no SLA via team score
	_, slaCount, _, err := db.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, slaCount, "expected no SLA violations")
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

	team1 := createTestTeam(t, "Team Multi 1", "01")
	team2 := createTestTeam(t, "Team Multi 2", "02")

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRoundStartTime = time.Now()

	// Team 1 fails, Team 2 passes - over 3 rounds
	for round := uint(1); round <= 3; round++ {
		engine.CurrentRound = round
		engine.processCollectedResults([]checks.Result{
			{TeamID: team1.ID, ServiceName: "svc", Status: false, RoundID: round},
			{TeamID: team2.ID, ServiceName: "svc", Status: true, Points: 10, RoundID: round},
		})
	}

	// Team 1 should have SLA (counter reset proves it triggered)
	assert.Equal(t, 0, engine.SlaPerService[team1.ID]["svc"],
		"Team 1 SLA counter should reset after triggering")

	// Verify via team scores
	_, slaCount1, _, _ := db.GetTeamScore(team1.ID)
	_, slaCount2, _, _ := db.GetTeamScore(team2.ID)
	assert.Equal(t, 1, slaCount1, "Team 1 should have 1 SLA")
	assert.Equal(t, 0, slaCount2, "Team 2 should have 0 SLAs")

	// Team 2 should have 100% uptime
	assert.Equal(t, 3, engine.UptimePerService[team2.ID]["svc"].PassedChecks)
	assert.Equal(t, 3, engine.UptimePerService[team2.ID]["svc"].TotalChecks)
}

// mockRunner is a simple runner for testing that always passes
type mockRunner struct {
	checks.Service
}

func (m *mockRunner) Run(teamID uint, identifier string, roundID uint, resultsChan chan checks.Result) {
	resultsChan <- checks.Result{
		TeamID:      teamID,
		ServiceName: m.Name,
		ServiceType: m.ServiceType,
		RoundID:     roundID,
		Status:      true,
		Points:      m.Points,
	}
}

func (m *mockRunner) Runnable() bool                  { return true }
func (m *mockRunner) GetType() string                 { return m.ServiceType }
func (m *mockRunner) GetName() string                 { return m.Name }
func (m *mockRunner) GetAttempts() int                { return 1 }
func (m *mockRunner) GetCredlists() []string          { return nil }
func (m *mockRunner) Verify(box, ip string, points, timeout, slapenalty, slathreshold int) error {
	m.Name = box + "-" + m.Service.Display
	m.Target = ip
	m.Points = points
	m.Timeout = timeout
	m.ServiceType = "Mock"
	return nil
}

func TestRvb_EnqueuesTasksAndCollectsResults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set Redis address for rvb() internal connections
	t.Setenv("REDIS_ADDR", "localhost:6379")

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	db.ResetScores()

	team1 := createTestTeam(t, "Team Rvb 1", "01")
	team2 := createTestTeam(t, "Team Rvb 2", "02")

	// Create engine with mock runner
	engine := newTestEngine(t, redis, 3)
	engine.CurrentRound = 1

	// Add a mock service to the config
	mockSvc := &mockRunner{
		Service: checks.Service{
			Display:     "web",
			Name:        "box01-web",
			ServiceType: "Mock",
			Points:      10,
		},
	}
	engine.Config.Box = []config.Box{
		{
			Name:    "box01",
			IP:      "10.0.0.1",
			Runners: []checks.Runner{mockSvc},
		},
	}

	// Start a goroutine that consumes tasks and pushes results
	stopRunner := make(chan struct{})
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		for {
			select {
			case <-stopRunner:
				return
			default:
				val, err := redis.Client.BLPop(ctx, time.Second, "tasks").Result()
				if err != nil {
					continue
				}
				if len(val) < 2 {
					continue
				}

				var task Task
				if err := json.Unmarshal([]byte(val[1]), &task); err != nil {
					t.Logf("Failed to unmarshal task: %v", err)
					continue
				}

				// Push a successful result
				result := checks.Result{
					TeamID:      task.TeamID,
					ServiceName: task.ServiceName,
					ServiceType: task.ServiceType,
					RoundID:     task.RoundID,
					Status:      true,
					Points:      10,
				}
				resultJSON, _ := json.Marshal(result)
				redis.Client.RPush(ctx, "results", resultJSON)
			}
		}
	}()

	// Run one round
	err := engine.rvb()
	close(stopRunner)
	<-runnerDone

	require.NoError(t, err)

	// Verify round was saved with results
	round, err := db.GetLastRound()
	require.NoError(t, err)
	assert.Equal(t, uint(1), round.ID)

	// Filter checks for our teams only (other teams may exist from previous tests)
	var teamChecks []db.ServiceCheckSchema
	for _, check := range round.Checks {
		if check.TeamID == team1.ID || check.TeamID == team2.ID {
			teamChecks = append(teamChecks, check)
		}
	}

	// Should have 2 checks (one per team)
	assert.Len(t, teamChecks, 2)

	// Both should pass
	for _, check := range teamChecks {
		assert.True(t, check.Result)
		assert.Equal(t, 10, check.Points)
	}
}

func TestRvb_HandlesMultipleServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Setenv("REDIS_ADDR", "localhost:6379")

	redis := testutil.StartRedis(t)
	defer redis.Close()

	pg := testutil.StartPostgres(t)
	defer pg.Close()
	db.Connect(pg.ConnectionString())

	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	db.ResetScores()

	team := createTestTeam(t, "Team Multi Svc", "01")

	engine := newTestEngine(t, redis, 3)
	engine.CurrentRound = 1

	// Add multiple services
	webSvc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	sshSvc := &mockRunner{Service: checks.Service{Display: "ssh", Name: "box01-ssh", ServiceType: "Mock", Points: 5}}
	dnsSvc := &mockRunner{Service: checks.Service{Display: "dns", Name: "box01-dns", ServiceType: "Mock", Points: 15}}

	engine.Config.Box = []config.Box{
		{
			Name:    "box01",
			IP:      "10.0.0.1",
			Runners: []checks.Runner{webSvc, sshSvc, dnsSvc},
		},
	}

	// Runner goroutine - returns different points based on service
	stopRunner := make(chan struct{})
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		for {
			select {
			case <-stopRunner:
				return
			default:
				val, err := redis.Client.BLPop(ctx, time.Second, "tasks").Result()
				if err != nil {
					continue
				}
				if len(val) < 2 {
					continue
				}

				var task Task
				json.Unmarshal([]byte(val[1]), &task)

				// Return points based on service name
				points := 10
				if task.ServiceName == "box01-ssh" {
					points = 5
				} else if task.ServiceName == "box01-dns" {
					points = 15
				}

				result := checks.Result{
					TeamID:      task.TeamID,
					ServiceName: task.ServiceName,
					ServiceType: task.ServiceType,
					RoundID:     task.RoundID,
					Status:      true,
					Points:      points,
				}
				resultJSON, _ := json.Marshal(result)
				redis.Client.RPush(ctx, "results", resultJSON)
			}
		}
	}()

	err := engine.rvb()
	close(stopRunner)
	<-runnerDone

	require.NoError(t, err)

	round, err := db.GetLastRound()
	require.NoError(t, err)

	// Filter checks for our team only (other teams may exist from previous tests)
	var teamChecks []db.ServiceCheckSchema
	for _, check := range round.Checks {
		if check.TeamID == team.ID {
			teamChecks = append(teamChecks, check)
		}
	}

	// Should have 3 checks (one per service) for our team
	assert.Len(t, teamChecks, 3)

	// Verify total points for our team: 10 + 5 + 15 = 30
	totalPoints := 0
	for _, check := range teamChecks {
		totalPoints += check.Points
	}
	assert.Equal(t, 30, totalPoints)
}
