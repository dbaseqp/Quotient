package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"time"

	"quotient/engine/checks"
	"quotient/engine/config"
	"quotient/engine/db"

	"github.com/redis/go-redis/v9"
)

type ScoringEngine struct {
	Config                *config.ConfigSettings
	CredentialsMutex      map[uint]*sync.Mutex
	UptimePerService      map[uint]map[string]db.Uptime
	SlaPerService         map[uint]map[string]int
	EnginePauseWg         *sync.WaitGroup
	IsEnginePaused        bool
	CurrentRound          uint
	NextRoundStartTime    time.Time
	CurrentRoundStartTime time.Time
	RedisClient           *redis.Client

	// signals
	ResetChan chan struct{}
}

func NewEngine(conf *config.ConfigSettings) *ScoringEngine {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient_redis:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("Failed to connect to Redis: %v", err))
	}

	return &ScoringEngine{
		Config:           conf,
		CredentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		ResetChan:        make(chan struct{}),
		RedisClient:      rdb,
	}
}

func (se *ScoringEngine) Start() {
	if t, err := db.GetLastRound(); err != nil {
		slog.Error("failed to get last round", "error", err)
	} else {
		se.CurrentRound = uint(t.ID) + 1
	}

	if err := db.LoadUptimes(&se.UptimePerService); err != nil {
		slog.Error("failed to load uptimes", "error", err)
	}

	if err := db.LoadSLAs(&se.SlaPerService, se.Config.MiscSettings.SlaThreshold); err != nil {
		slog.Error("failed to load SLAs", "error", err)
	}

	// load credentials
	err := se.LoadCredentials()
	if err != nil {
		slog.Error("failed to load credential files into teams", "error", err)
	}

	// start paused if configured
	se.IsEnginePaused = false
	se.EnginePauseWg = &sync.WaitGroup{}
	if se.Config.MiscSettings.StartPaused {
		se.IsEnginePaused = true
		se.EnginePauseWg.Add(1)
	}

	se.NextRoundStartTime = time.Time{}

	// engine loop
	go func() {
		for {
			slog.Info("Queueing up for round", "round", se.CurrentRound)
			se.EnginePauseWg.Wait()
			slog.Info("Starting round", "round", se.CurrentRound)
			se.CurrentRoundStartTime = time.Now()
			se.NextRoundStartTime = time.Now().Add(time.Duration(se.Config.MiscSettings.Delay) * time.Second)

			// run the round logic
			switch se.Config.RequiredSettings.EventType {
			case "koth":
				se.koth()
			case "rvb":
				se.rvb()
			default:
				slog.Error("Unknown event type", "eventType", se.Config.RequiredSettings.EventType)
			}

			slog.Info(fmt.Sprintf("Round %d complete", se.CurrentRound))
			se.CurrentRound++

			se.RedisClient.Publish(context.Background(), "events", "round_finish")
			slog.Info(fmt.Sprintf("Round %d will start in %s, sleeping...", se.CurrentRound, time.Until(se.NextRoundStartTime).String()))
			time.Sleep(time.Until(se.NextRoundStartTime))
		}
		// wait for first signal of done round (either from reset or end of round)
	}()
	<-se.ResetChan
	slog.Info("engine loop ending (probably due to reset)")
	// this return should kill any running goroutines by breaking the loop
}

func (se *ScoringEngine) GetUptimePerService() map[uint]map[string]db.Uptime {
	return se.UptimePerService
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

func (se *ScoringEngine) PauseEngine() {
	if !se.IsEnginePaused {
		se.EnginePauseWg.Add(1)
		se.IsEnginePaused = true
	}
}

func (se *ScoringEngine) ResumeEngine() {
	if se.IsEnginePaused {
		se.EnginePauseWg.Done()
		se.IsEnginePaused = false
	}
}

// ResetScores resets the engine to the initial state and stops the engine
func (se *ScoringEngine) ResetScores() error {
	slog.Info("Resetting scores and clearing Redis queues")

	// Stop the engine
	se.ResetChan <- struct{}{}

	// Reset the database
	if err := db.ResetScores(); err != nil {
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
	se.CurrentRound = 1
	se.UptimePerService = make(map[uint]map[string]db.Uptime)
	se.SlaPerService = make(map[uint]map[string]int)
	slog.Info("Scores reset and Redis queues cleared successfully")

	return nil
}

// perform a round of koth
func (se *ScoringEngine) koth() {
	// enginePauseWg.Wait()

	// do koth stuff
}

// perform a round of rvb
func (se *ScoringEngine) rvb() {
	// reassign the next round start time with jitter
	// double the jitter and subtract it to get a random number between -jitter and jitter
	randomJitter := rand.Intn(2*se.Config.MiscSettings.Jitter) - se.Config.MiscSettings.Jitter
	jitter := time.Duration(randomJitter) * time.Second
	se.NextRoundStartTime = time.Now().Add(time.Duration(se.Config.MiscSettings.Delay) * time.Second).Add(jitter)

	slog.Info(fmt.Sprintf("round should take %s", time.Until(se.NextRoundStartTime).String()))

	// do rvb stuff
	teams, err := db.GetTeams()
	if err != nil {
		slog.Error("failed to get teams:", "error", err)
		return
	}

	runners := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Until(se.NextRoundStartTime))
	defer cancel()

	slog.Debug("Starting service checks", "round", se.CurrentRound)
	allRunners := se.Config.AllChecks()

	// 1) Enqueue
	for _, team := range teams {
		if !team.Active {
			continue
		}
		for _, r := range allRunners {
			if !r.Runnable() {
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
				RoundID:        se.CurrentRound,
				Deadline:       se.NextRoundStartTime,
				Attempts:       r.GetAttempts(),
				CheckData:      data, // the entire specialized struct
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

	// 2) Collect results from Redis
	results := make([]checks.Result, 0, runners)
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Until(se.NextRoundStartTime))
	defer cancel()

	i := 0
	for i < runners {
		val, err := se.RedisClient.BLPop(timeoutCtx, time.Until(se.NextRoundStartTime), "results").Result()
		if err == redis.Nil {
			slog.Warn("Timeout waiting for results", "remaining", runners-i)
			// Clear the results, since we didn't collect everything in time
			results = []checks.Result{}
			break
		} else if err != nil {
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
		if result.RoundID != se.CurrentRound {
			slog.Warn("Ignoring out of round result", "receivedRound", result.RoundID, "currentRound", se.CurrentRound)
			continue
		}
		results = append(results, result)
		i++
		slog.Debug("service check finished", "round_id", result.RoundID, "team_id", result.TeamID, "service_name", result.ServiceName, "result", result.Status, "debug", result.Debug, "error", result.Error)
	}

	// 3) Process all collected results
	se.processCollectedResults(results)
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
			ServiceName: result.ServiceName,
			Points:      result.Points,
			Result:      result.Status,
			Error:       result.Error,
			Debug:       result.Debug,
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
	if _, err := db.CreateRound(round); err != nil {
		slog.Error("failed to create round:", "round", se.CurrentRound, "error", err)
		return
	}

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
					Penalty:     se.Config.MiscSettings.SlaPenalty,
				}
				db.CreateSLA(sla)
				se.SlaPerService[result.TeamID][result.ServiceName] = 0
			}
		}
	}

	slog.Debug("Successfully processed results for round", "round", se.CurrentRound, "total", len(dbResults))
}
