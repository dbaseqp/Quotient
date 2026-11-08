package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/redis/go-redis/v9"
)

type ScoringEngine struct {
	Config *config.ConfigSettings
	// credentialsMutex holds one lock per team. Seeding runs on the engine
	// goroutine while the web server serves, so credentialsMu guards the map.
	credentialsMutex      map[uint]*sync.Mutex
	credentialsMu         sync.RWMutex
	UptimePerService      map[uint]map[string]db.Uptime
	uptimeMu              sync.RWMutex
	SlaPerService         map[uint]map[string]int
	CurrentRound          uint
	PreflightRound        uint // last preflight round run; see preflight()
	NextRoundStartTime    time.Time
	CurrentRoundStartTime time.Time
	RedisClient           *redis.Client
	DB                    *db.DB

	// Concurrency control for materialized view refresh
	Refreshing atomic.Bool

	// Config update handling
	configPath string

	pauseMu sync.Mutex
	// resume is closed while the engine runs; while paused it is open and the round loop blocks on it.
	resume chan struct{}
}

func NewEngine(conf *config.ConfigSettings, configPath string) *ScoringEngine {
	// Ensure config is properly validated and runners are set up
	if err := conf.SetConfig(configPath); err != nil {
		panic(fmt.Sprintf("Failed to validate initial config: %v", err))
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient-redis:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("Failed to connect to Redis: %v", err))
	}

	d := db.Connect(conf.RequiredSettings.DBConnectURL)

	if err := d.AddTeams(conf); err != nil {
		panic(fmt.Sprintf("Failed to add teams to DB: %v", err))
	}

	se := &ScoringEngine{
		Config:           conf,
		credentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		RedisClient:      rdb,
		configPath:       configPath,
		DB:               d,
	}
	se.resetPauseState()

	if err := conf.WatchConfig(configPath, func() {
		if err := se.DB.AddTeams(se.Config); err != nil {
			slog.Error("failed to add teams on config reload", "error", err)
		}
		if err := se.EnsureCredentialsSeeded(); err != nil {
			slog.Error("failed to seed credentials on config reload", "error", err)
		}
	}); err != nil {
		slog.Error("Failed to start config watcher", "error", err)
	}

	return se
}

func (se *ScoringEngine) Start() {
	if t, err := se.DB.GetLastRound(); err != nil {
		slog.Error("failed to get last round", "error", err)
	} else {
		se.CurrentRound = uint(t.ID) + 1
	}
	if last, err := se.DB.GetLastPreflightRound(); err != nil {
		slog.Error("failed to get last preflight round", "error", err)
	} else {
		se.PreflightRound = last
	}

	se.uptimeMu.Lock()
	if err := se.DB.LoadUptimes(&se.UptimePerService); err != nil {
		slog.Error("failed to load uptimes", "error", err)
	}
	se.uptimeMu.Unlock()

	if err := se.DB.LoadSLAs(&se.SlaPerService, se.Config.MiscSettings.SlaThreshold); err != nil {
		slog.Error("failed to load SLAs", "error", err)
	}

	// load credentials
	err := se.EnsureCredentialsSeeded()
	if err != nil {
		slog.Error("failed to load credential files into teams", "error", err)
	}

	se.NextRoundStartTime = time.Time{}

	stop := make(chan struct{})
	loopDone := make(chan struct{})

	// engine loop
	go func() {
		defer close(loopDone)
		for {
			slog.Info("Queueing up for round", "round", se.CurrentRound)
			se.pauseMu.Lock()
			resume := se.resume
			se.pauseMu.Unlock()
			select {
			case <-stop:
				return
			case <-resume:
			}
			select {
			case <-stop:
				return
			default:
			}

			preflight := se.Config.RequiredSettings.EventType == "rvb" && !se.competitionEverStarted()
			se.CurrentRoundStartTime = time.Now()
			se.NextRoundStartTime = time.Now().Add(time.Duration(se.Config.MiscSettings.Delay) * time.Second)

			var err error
			switch {
			case preflight:
				slog.Info("Starting preflight round", "preflight_round", se.PreflightRound+1)
				err = se.preflight()
			case se.Config.RequiredSettings.EventType == "koth":
				slog.Info("Starting round", "round", se.CurrentRound)
				err = se.koth()
			case se.Config.RequiredSettings.EventType == "rvb":
				slog.Info("Starting round", "round", se.CurrentRound)
				err = se.rvb()
			default:
				slog.Error("Unknown event type", "eventType", se.Config.RequiredSettings.EventType)
			}
			if err != nil {
				slog.Error("Round error. If this is a reset, ignore...", "error", err)
				return
			}

			if preflight {
				slog.Info(fmt.Sprintf("Preflight round %d complete", se.PreflightRound))
			} else {
				slog.Info(fmt.Sprintf("Round %d complete", se.CurrentRound))
				se.CurrentRound++
				se.RedisClient.Publish(context.Background(), "events", "round_finish")
			}
			slog.Info(fmt.Sprintf("Next round will start in %s, sleeping...", time.Until(se.NextRoundStartTime).String()))
			select {
			case <-stop:
				return
			case <-time.After(time.Until(se.NextRoundStartTime)):
			}
		}
	}()
	se.waitForReset()
	close(stop)
	<-loopDone
	slog.Info("Restarting scoring...")
}

func (se *ScoringEngine) waitForReset() {
	// wait for a signal to reset the engine
	// this will block until the engine is reset
	// this is a blocking call
	// the engine will be reset and the loop will start again

	events := se.RedisClient.Subscribe(context.Background(), "events")
	// nolint:errcheck
	defer events.Close()
	eventsChannel := events.Channel()

	for msg := range eventsChannel {
		slog.Info("Received message", "message", msg.Payload)
		if msg.Payload == "reset" {
			slog.Info("Reset event received, quitting...")
			return
		} else {
			continue
		}
	}
}

func (se *ScoringEngine) GetUptimePerService() map[uint]map[string]db.Uptime {
	return se.UptimePerService
}

func (se *ScoringEngine) RLockUptime() {
	se.uptimeMu.RLock()
}

func (se *ScoringEngine) RUnlockUptime() {
	se.uptimeMu.RUnlock()
}

// GetActiveTasks returns all active and recently completed tasks
func (se *ScoringEngine) GetActiveTasks() (map[string]any, error) {
	ctx := context.Background()

	// Default empty response structure
	result := map[string]any{
		"running":     []any{},
		"success":     []any{},
		"failed":      []any{},
		"all_runners": []any{},
	}

	// Get all task keys using a single pattern
	allKeys, err := se.RedisClient.Keys(ctx, "task:*").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks: %w", err)
	}

	// Skip if no keys found
	if len(allKeys) == 0 {
		return result, nil
	}

	// Use a single pipeline to get all task data in one round-trip
	pipe := se.RedisClient.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(allKeys))

	// Queue up all GET commands at once
	for _, key := range allKeys {
		cmds[key] = pipe.Get(ctx, key)
	}

	// Execute the pipeline
	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to execute pipeline: %w", err)
	}

	// Use map as a set to track unique runner IDs
	runnersSet := make(map[string]struct{})

	// Process all results
	for _, cmd := range cmds {
		value, err := cmd.Result()
		if err != nil {
			continue // Skip if we can't get the task details
		}

		// Parse the value as JSON directly into a checks.Result
		var taskStatus checks.Result
		if err := json.Unmarshal([]byte(value), &taskStatus); err != nil {
			continue // Skip if we can't parse the JSON
		}

		// Add runner ID to set if available
		if taskStatus.RunnerID != "" {
			runnersSet[taskStatus.RunnerID] = struct{}{}
		}

		// Categorize by status - directly using the status as map key for cleaner code
		if statusKey := taskStatus.StatusText; statusKey == "running" || statusKey == "success" || statusKey == "failed" {
			result[statusKey] = append(result[statusKey].([]any), taskStatus)
		}
	}

	// Convert runners set to slice in one go
	for runnerId := range runnersSet {
		result["all_runners"] = append(result["all_runners"].([]any), runnerId)
	}

	return result, nil
}

func (se *ScoringEngine) resetPauseState() {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	se.resume = make(chan struct{})
	if !se.Config.MiscSettings.StartPaused {
		close(se.resume)
	}
}

func (se *ScoringEngine) pausedLocked() bool {
	select {
	case <-se.resume:
		return false
	default:
		return true
	}
}

func (se *ScoringEngine) SetPaused(pause bool) bool {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	if se.pausedLocked() == pause {
		return false
	}
	if pause {
		se.resume = make(chan struct{})
		return true
	}
	close(se.resume)
	return true
}

func (se *ScoringEngine) Paused() bool {
	se.pauseMu.Lock()
	defer se.pauseMu.Unlock()
	return se.pausedLocked()
}

// Stopping the competition after it starts doesn't bring preflight rounds back.
func (se *ScoringEngine) competitionEverStarted() bool {
	start, err := se.DB.GetCompetitionStart()
	if err != nil {
		slog.Error("failed to read competition start; scoring the round", "error", err)
		return true
	}
	return start != nil
}

// ResetScores resets the engine to the initial state and stops the engine
func (se *ScoringEngine) ResetScores() error {
	slog.Info("Resetting scores and clearing Redis queues")

	// Reset the database
	if err := se.DB.ResetScores(); err != nil {
		slog.Error("failed to reset scores", "error", err)
		return fmt.Errorf("failed to reset scores: %v", err)
	}

	// Flush Redis queues
	ctx := context.Background()
	keysToDelete := []string{"tasks", "results"}
	for _, key := range keysToDelete {
		if err := se.RedisClient.Del(ctx, key).Err(); err != nil {
			slog.Error("Failed to clear Redis queue", "queue", key, "error", err)
			return fmt.Errorf("failed to clear Redis queue %s: %v", key, err)
		}
	}

	// Reset engine state
	se.RedisClient.Publish(context.Background(), "events", "reset")

	se.CurrentRound = 1
	se.uptimeMu.Lock()
	se.UptimePerService = make(map[uint]map[string]db.Uptime)
	se.uptimeMu.Unlock()
	se.SlaPerService = make(map[uint]map[string]int)
	slog.Info("Scores reset and Redis queues cleared successfully")

	return nil
}

// perform a round of koth
func (se *ScoringEngine) koth() error {
	// enginePauseWg.Wait()

	// do koth stuff
	return nil
}

func (se *ScoringEngine) rvb() error {
	results, err := se.runChecks(se.CurrentRound, false)
	if err != nil {
		return err
	}
	se.processCollectedResults(results)
	return nil
}

func (se *ScoringEngine) preflight() error {
	round := se.PreflightRound + 1
	results, err := se.runChecks(round, true)
	if err != nil {
		return err
	}
	se.PreflightRound = round
	if len(results) == 0 {
		slog.Warn("No results collected for preflight round", "preflight_round", round)
		return nil
	}

	rows := make([]db.PreflightCheckSchema, 0, len(results))
	for _, result := range results {
		rows = append(rows, db.PreflightCheckSchema{
			RoundID:     round,
			StartTime:   se.CurrentRoundStartTime,
			TeamID:      result.TeamID,
			ServiceName: sanitizeDBString(result.ServiceName),
			ServiceType: result.ServiceType,
			Result:      result.Status,
			Error:       sanitizeDBString(result.Error),
			Debug:       sanitizeDBString(result.Debug),
		})
	}
	if err := se.DB.CreatePreflightChecks(rows); err != nil {
		slog.Error("failed to save preflight round", "preflight_round", round, "error", err)
	}
	return nil
}

func (se *ScoringEngine) runChecks(roundID uint, preflight bool) ([]checks.Result, error) {
	// reassign the next round start time with jitter
	// double the jitter and subtract it to get a random number between -jitter and jitter
	randomJitter := rand.Intn(2*se.Config.MiscSettings.Jitter) - se.Config.MiscSettings.Jitter // #nosec G404 -- non-crypto randomization for timing jitter
	jitter := time.Duration(randomJitter) * time.Second
	se.NextRoundStartTime = time.Now().Add(time.Duration(se.Config.MiscSettings.Delay) * time.Second).Add(jitter)

	slog.Info(fmt.Sprintf("round should take %s", time.Until(se.NextRoundStartTime).String()))

	events := se.RedisClient.Subscribe(context.Background(), "events")
	// nolint:errcheck
	defer events.Close()
	eventsChannel := events.Channel()

	teams, err := se.DB.GetTeams()
	if err != nil {
		slog.Error("failed to get teams:", "error", err)
		return nil, err
	}

	runners := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Until(se.NextRoundStartTime))
	defer cancel()

	slog.Debug("Starting service checks", "round", roundID, "preflight", preflight)
	allRunners := se.Config.AllChecks()
	slog.Info("Service check configuration",
		"boxes", len(se.Config.Box),
		"runners", len(allRunners))

	// Log details about each box's runners
	for _, box := range se.Config.Box {
		slog.Debug("Box configuration", "name", box.Name, "runners", len(box.Runners))
	}

	// Clear any stale tasks and results before enqueuing new ones. After a
	// restart mid-round the next round reuses the round ID, so leftover
	// results would otherwise be counted as this round's.
	for _, key := range []string{"tasks", "results"} {
		if stale := se.RedisClient.LLen(ctx, key).Val(); stale > 0 {
			slog.Warn("Clearing stale entries from queue", "queue", key, "count", stale, "round", roundID, "preflight", preflight)
			se.RedisClient.Del(ctx, key)
		}
	}

	for _, team := range teams {
		if !team.Active {
			continue
		}
		for _, r := range allRunners {
			if !r.Runnable() {
				continue
			}
			enabled, err := se.DB.IsTeamServiceEnabled(team.ID, r.GetName())
			if err != nil {
				slog.Error("failed to check service state", "team", team.ID, "service", r.GetName(), "error", err)
				continue
			}
			if !enabled {
				continue
			}
			// serialize the entire check definition to JSON
			data, err := json.Marshal(r)
			if err != nil {
				slog.Error("failed to marshal check definition", "error", err)
				continue
			}

			task := Task{
				TeamID:         team.ID,
				TeamIdentifier: team.Identifier,
				ServiceType:    r.GetType(),
				ServiceName:    r.GetName(),
				RoundID:        roundID,
				Preflight:      preflight,
				Deadline:       se.NextRoundStartTime,
				Attempts:       r.GetAttempts(),
				AttemptDelay:   r.GetAttemptDelay(),
				CheckData:      data, // the entire specialized struct
			}

			// Populate credentials if check needs them
			if credlists := r.GetCredlists(); len(credlists) > 0 {
				for _, credlistName := range credlists {
					creds, err := se.DB.GetTeamCredentials(team.ID, credlistName)
					if err != nil {
						slog.Error("failed to get credentials for task", "team", team.ID, "credlist", credlistName, "error", err)
						continue
					}
					for _, c := range creds {
						task.Credentials = append(task.Credentials, Credential{
							Username: c.Username,
							Password: c.Password,
						})
					}
				}
				// Skip task if check requires credentials but none were loaded
				if len(task.Credentials) == 0 {
					slog.Error("skipping check: requires credentials but none seeded", "service", r.GetName(), "team", team.ID)
					continue
				}
			}

			payload, err := json.Marshal(task)
			if err != nil {
				slog.Error("failed to marshal service task", "error", err)
				continue
			}
			se.RedisClient.RPush(ctx, "tasks", payload)
			runners++
		}
	}
	slog.Info("Enqueued checks", "count", runners)

	results := make([]checks.Result, 0, runners)
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Until(se.NextRoundStartTime))
	defer cancel()

	i := 0
COLLECTION:
	for i < runners {
		select {
		case msg := <-eventsChannel:
			slog.Info("Received message", "message", msg.Payload)
			if msg.Payload == "reset" {
				slog.Info("Reset event received, quitting...")
				return nil, fmt.Errorf("reset event received")
			} else {
				continue
			}
		default:
			val, err := se.RedisClient.BLPop(timeoutCtx, time.Until(se.NextRoundStartTime), "results").Result()
			if err == redis.Nil {
				slog.Warn("Timeout waiting for results", "remaining", runners-i, "collected", i, "expected", runners)
				results = []checks.Result{}
				break COLLECTION
			} else if err != nil {
				// Check if the timeout context has expired
				if timeoutCtx.Err() != nil {
					slog.Warn("Round deadline exceeded while waiting for results", "remaining", runners-i, "collected", i, "expected", runners, "error", err)
					results = []checks.Result{}
					break COLLECTION
				}
				slog.Error("Failed to fetch results from Redis:", "error", err)
				time.Sleep(2 * time.Second)
				continue
			}

			// val[0] = "results", val[1] = JSON checks.Result
			if len(val) < 2 {
				slog.Warn("Malformed result from Redis", "val", val)
				continue
			}
			raw := val[1]

			var result checks.Result
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				slog.Error("Failed to unmarshal check result", "error", err)
				continue
			}
			if result.RoundID != roundID || result.Preflight != preflight {
				slog.Warn("Ignoring out of round result", "receivedRound", result.RoundID, "receivedPreflight", result.Preflight, "currentRound", roundID, "currentPreflight", preflight)
				continue
			}
			results = append(results, result)
			i++
			slog.Debug("service check finished", "round_id", result.RoundID, "team_id", result.TeamID, "service_name", result.ServiceName, "result", result.Status, "debug", result.Debug, "error", result.Error)
		}
	}

	return results, nil
}

func sanitizeDBString(s string) string {
	// remove nulls
	s = strings.ReplaceAll(s, "\x00", "")
	// if invalid UTF-8, replace with �
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

func (se *ScoringEngine) processCollectedResults(results []checks.Result) {
	if len(results) == 0 {
		slog.Warn("No results collected for round", "round", se.CurrentRound)
		return
	}

	dbResults := []db.ServiceCheckSchema{}

	for _, result := range results {
		dbResults = append(dbResults, db.ServiceCheckSchema{
			TeamID:      result.TeamID,
			RoundID:     uint(se.CurrentRound),
			ServiceName: sanitizeDBString(result.ServiceName),
			Points:      result.Points,
			Result:      result.Status,
			Error:       sanitizeDBString(result.Error),
			Debug:       sanitizeDBString(result.Debug),
		})
	}

	if len(dbResults) == 0 {
		slog.Warn("No results to process for the current round", "round", se.CurrentRound)
		return
	}

	// Save results to database
	round := db.RoundSchema{
		ID:        uint(se.CurrentRound),
		StartTime: se.CurrentRoundStartTime,
		Checks:    dbResults,
	}
	if _, err := se.DB.CreateRound(round); err != nil {
		slog.Error("failed to create round:", "round", se.CurrentRound, "error", err)
		return
	}

	se.uptimeMu.Lock()
	for _, result := range results {
		// Update uptime and SLA maps
		if _, ok := se.UptimePerService[result.TeamID]; !ok {
			se.UptimePerService[result.TeamID] = make(map[string]db.Uptime)
		}
		if _, ok := se.UptimePerService[result.TeamID][result.ServiceName]; !ok {
			se.UptimePerService[result.TeamID][result.ServiceName] = db.Uptime{}
		}
		newUptime := se.UptimePerService[result.TeamID][result.ServiceName]
		if result.Status {
			newUptime.PassedChecks++
		}
		newUptime.TotalChecks++
		se.UptimePerService[result.TeamID][result.ServiceName] = newUptime

		if _, ok := se.SlaPerService[result.TeamID]; !ok {
			se.SlaPerService[result.TeamID] = make(map[string]int)
		}
		if _, ok := se.SlaPerService[result.TeamID][result.ServiceName]; !ok {
			se.SlaPerService[result.TeamID][result.ServiceName] = 0
		}
		if result.Status {
			se.SlaPerService[result.TeamID][result.ServiceName] = 0
		} else {
			se.SlaPerService[result.TeamID][result.ServiceName]++
			if se.SlaPerService[result.TeamID][result.ServiceName] >= se.Config.MiscSettings.SlaThreshold {
				sla := db.SLASchema{
					TeamID:      result.TeamID,
					ServiceName: result.ServiceName,
					RoundID:     uint(se.CurrentRound),
					Penalty:     *se.Config.MiscSettings.SlaPenalty,
				}
				if _, err := se.DB.CreateSLA(sla); err != nil {
					slog.Error("failed to create SLA", "team", result.TeamID, "service", result.ServiceName, "error", err)
				}
				se.SlaPerService[result.TeamID][result.ServiceName] = 0
			}
		}
	}
	se.uptimeMu.Unlock()

	slog.Debug("Successfully processed results for round", "round", se.CurrentRound, "total", len(dbResults))

	// Refresh materialized view asynchronously, but avoid concurrent refreshes
	currentRound := se.CurrentRound
	if se.Refreshing.CompareAndSwap(false, true) {
		go func(round uint) {
			defer se.Refreshing.Store(false)
			if err := se.DB.RefreshScoresMaterializedView(); err != nil {
				slog.Error("failed to refresh materialized view", "round", round, "error", err)
			}
		}(currentRound)
	} else {
		slog.Debug("refresh already in progress, skipping refresh spawn", "round", currentRound)
	}
}

func NewTestEngine(t *testing.T, redis *testutil.RedisContainer, pg *testutil.PostgresContainer, slaThreshold int) *ScoringEngine {
	t.Helper()

	slaPenalty := 30
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
			SlaPenalty:   &slaPenalty,
		},
	}

	se := &ScoringEngine{
		Config:           conf,
		credentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		RedisClient:      redis.Client,
		DB:               pg.DB,
		CurrentRound:     1,
	}
	se.resetPauseState()
	return se
}
