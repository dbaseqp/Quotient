package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessCollectedResults_SavesRound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	// Clean slate
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team", "01")

	engine := NewTestEngine(t, redis, pg, 3)
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
	round, err := pg.DB.GetLastRound()
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

	redis, pg := testutil.StartContainers(t)

	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team", "01")

	engine := NewTestEngine(t, redis, pg, 3)
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

	redis, pg := testutil.StartContainers(t)

	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team SLA", "01")

	// SLA threshold of 3 consecutive failures
	engine := NewTestEngine(t, redis, pg, 3)
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
	_, slaCount, _, err := pg.DB.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, slaCount, "expected 1 SLA violation")
}

func TestProcessCollectedResults_SLAResetsOnPass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team SLA Reset", "01")

	engine := NewTestEngine(t, redis, pg, 3)
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
	_, slaCount, _, err := pg.DB.GetTeamScore(team.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, slaCount, "expected no SLA violations")
}

func TestProcessCollectedResults_MultipleTeamsIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())

	team1 := pg.CreateTestTeam(t, "Team Multi 1", "01")
	team2 := pg.CreateTestTeam(t, "Team Multi 2", "02")

	engine := NewTestEngine(t, redis, pg, 3)
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
	_, slaCount1, _, _ := pg.DB.GetTeamScore(team1.ID)
	_, slaCount2, _, _ := pg.DB.GetTeamScore(team2.ID)
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

func (m *mockRunner) Runnable() bool         { return true }
func (m *mockRunner) GetType() string        { return m.ServiceType }
func (m *mockRunner) GetName() string        { return m.Name }
func (m *mockRunner) GetAttempts() int       { return 1 }
func (m *mockRunner) GetCredlists() []string { return nil }
func (m *mockRunner) Verify(box, ip string, points, timeout, slapenalty, slathreshold int) error {
	m.Name = box + "-" + m.Display
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

	redis, pg := testutil.StartContainers(t)

	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	team1 := pg.CreateTestTeam(t, "Team Rvb 1", "01")
	team2 := pg.CreateTestTeam(t, "Team Rvb 2", "02")

	// Create engine with mock runner
	engine := NewTestEngine(t, redis, pg, 3)
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
	round, err := pg.DB.GetLastRound()
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

func TestRvb_StopsOnResetPublishedToEngineRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	pg.CreateTestTeam(t, "Team Reset 1", "01")
	pg.CreateTestTeam(t, "Team Reset 2", "02")

	engine := NewTestEngine(t, redis, pg, 3)
	mockSvc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	engine.Config.Box = []config.Box{{Name: "box01", IP: "10.0.0.1", Runners: []checks.Runner{mockSvc}}}

	// Once rvb is subscribed, publish a reset on the engine's own Redis, then
	// return one result so the collection loop checks for events again.
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		subscribeDeadline := time.Now().Add(2 * time.Second)
		for redis.Client.PubSubNumSub(ctx, "events").Val()["events"] == 0 {
			if time.Now().After(subscribeDeadline) {
				return // rvb never subscribed here, so the reset can't reach it
			}
			time.Sleep(10 * time.Millisecond)
		}
		redis.Client.Publish(ctx, "events", "reset")
		time.Sleep(200 * time.Millisecond)

		val, err := redis.Client.BLPop(ctx, 2*time.Second, "tasks").Result()
		if err != nil {
			return
		}
		var task Task
		if json.Unmarshal([]byte(val[1]), &task) != nil {
			return
		}
		result, _ := json.Marshal(checks.Result{TeamID: task.TeamID, ServiceName: task.ServiceName, ServiceType: task.ServiceType, RoundID: task.RoundID, Status: true, Points: 10})
		redis.Client.RPush(ctx, "results", result)
	}()

	err := engine.rvb()
	<-runnerDone
	require.ErrorContains(t, err, "reset event received")
}

func TestRvb_HandlesMultipleServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	ctx := context.Background()
	redis.Client.FlushDB(ctx)
	require.NoError(t, pg.DB.ResetScores())

	team := pg.CreateTestTeam(t, "Team Multi Svc", "01")

	engine := NewTestEngine(t, redis, pg, 3)
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
				require.NoError(t, json.Unmarshal([]byte(val[1]), &task))

				// Return points based on service name
				points := 10
				switch task.ServiceName {
				case "box01-ssh":
					points = 5
				case "box01-dns":
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

	round, err := pg.DB.GetLastRound()
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

func TestEnsureCredentialsSeeded_HotReload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)

	dir := t.TempDir()
	credlistsDir := filepath.Join(dir, "credlists")
	require.NoError(t, os.MkdirAll(credlistsDir, 0o750))

	linuxCreds := filepath.Join(credlistsDir, "reload_linux.credlist")
	require.NoError(t, os.WriteFile(linuxCreds, []byte("alice,pass1\nbob,pass2\n"), 0o600))

	se := NewTestEngine(t, redis, pg, 3)
	se.configPath = filepath.Join(dir, "event.conf")
	se.Config.Team = []config.Team{
		{Name: "ReloadTeam1", Pw: "pw1"},
		{Name: "ReloadTeam2", Pw: "pw2"},
	}
	se.Config.CredlistSettings.Credlist = []config.Credlist{
		{CredlistName: "Linux", CredlistPath: "reload_linux.credlist"},
	}

	require.NoError(t, pg.DB.AddTeams(se.Config))
	require.NoError(t, se.EnsureCredentialsSeeded())

	teamsByName := func() map[string]db.TeamSchema {
		teams, err := pg.DB.GetTeams()
		require.NoError(t, err)
		m := make(map[string]db.TeamSchema, len(teams))
		for _, tm := range teams {
			m[tm.Name] = tm
		}
		return m
	}

	credMap := func(teamID uint, credlist string) map[string]string {
		creds, err := pg.DB.GetTeamCredentials(teamID, credlist)
		require.NoError(t, err)
		m := make(map[string]string, len(creds))
		for _, c := range creds {
			m[c.Username] = c.Password
		}
		return m
	}

	teams := teamsByName()
	team1 := teams["ReloadTeam1"]
	team2 := teams["ReloadTeam2"]
	require.NotZero(t, team1.ID)
	require.NotZero(t, team2.ID)

	assert.Equal(t, map[string]string{"alice": "pass1", "bob": "pass2"}, credMap(team1.ID, "reload_linux.credlist"))
	assert.Equal(t, map[string]string{"alice": "pass1", "bob": "pass2"}, credMap(team2.ID, "reload_linux.credlist"))

	updated, skipped, err := se.UpdateCredentials(team1.ID, "reload_linux.credlist", []string{"alice"}, []string{"team1custom"}, "team1")
	require.NoError(t, err)
	assert.Equal(t, 1, updated)
	assert.Empty(t, skipped)

	require.NoError(t, os.WriteFile(linuxCreds, []byte("alice,pass1_updated\ncharlie,pass3\n"), 0o600))
	winCreds := filepath.Join(credlistsDir, "reload_win.credlist")
	require.NoError(t, os.WriteFile(winCreds, []byte("admin,winpass\n"), 0o600))
	se.Config.CredlistSettings.Credlist = append(se.Config.CredlistSettings.Credlist,
		config.Credlist{CredlistName: "Windows", CredlistPath: "reload_win.credlist"},
	)
	se.Config.Team = append(se.Config.Team, config.Team{Name: "ReloadTeam3", Pw: "pw3"})

	require.NoError(t, pg.DB.AddTeams(se.Config))
	require.NoError(t, se.EnsureCredentialsSeeded())

	teams = teamsByName()
	team3 := teams["ReloadTeam3"]
	require.NotZero(t, team3.ID)

	// Team 1 keeps its PCR-rotated password for alice, gets charlie, and drops removed bob
	assert.Equal(t, map[string]string{
		"alice":   "team1custom",
		"charlie": "pass3",
	}, credMap(team1.ID, "reload_linux.credlist"))

	// Team 2 (which never rotated alice) gets the updated baseline password and audit entry
	assert.Equal(t, map[string]string{
		"alice":   "pass1_updated",
		"charlie": "pass3",
	}, credMap(team2.ID, "reload_linux.credlist"))
	history, err := se.GetPCRHistory(team2.ID, "reload_linux.credlist", "alice")
	require.NoError(t, err)
	require.NotEmpty(t, history)
	assert.Equal(t, "system", history[0].ChangedBy)
	assert.Equal(t, "pass1", history[0].OldPassword)
	assert.Equal(t, "pass1_updated", history[0].NewPassword)

	// Newly added Team 3 is seeded with active credentials and has a working credential lock
	assert.Equal(t, map[string]string{
		"alice":   "pass1_updated",
		"charlie": "pass3",
	}, credMap(team3.ID, "reload_linux.credlist"))
	updated3, _, err := se.UpdateCredentials(team3.ID, "reload_linux.credlist", []string{"alice"}, []string{"team3custom"}, "team3")
	require.NoError(t, err)
	assert.Equal(t, 1, updated3)
	assert.Equal(t, "team3custom", credMap(team3.ID, "reload_linux.credlist")["alice"])

	for _, tm := range []db.TeamSchema{team1, team2, team3} {
		assert.Equal(t, map[string]string{"admin": "winpass"}, credMap(tm.ID, "reload_win.credlist"))
	}

	// Resetting Team 1's credlist uses the updated baseline password
	require.NoError(t, se.ResetCredentials(team1.ID, "reload_linux.credlist", "admin"))
	assert.Equal(t, "pass1_updated", credMap(team1.ID, "reload_linux.credlist")["alice"])
}
