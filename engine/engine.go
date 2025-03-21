package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	waitForReset()
	slog.Info("engine loop ending (probably due to reset)")
	// this return should kill any running goroutines by breaking the loop
}

func waitForReset() {
	// wait for a signal to reset the engine
	// this will block until the engine is reset
	// this is a blocking call
	// the engine will be reset and the loop will start again

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "quotient_redis:6379"
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx := context.Background()

	for {
		val, err := rdb.BLPop(ctx, 0, "events").Result()
		if err != nil {
			log.Printf("[Runner] Error getting event: %v", err)
			continue
		}

		if len(val) < 2 {
			log.Printf("[Runner] Invalid BLPop response: %v", val)
			continue
		}

		if val[1] != "reset" {
			log.Printf("[Runner] Invalid event payload: %v", val[1])
			continue
		}

		log.Printf("[Runner] Reset event received, quitting...")
		return
	}
}

func (se *ScoringEngine) GetUptimePerService() map[uint]map[string]db.Uptime {
	return se.UptimePerService
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

// ResetEngine resets the engine to the initial state and stops the engine
func (se *ScoringEngine) ResetScores() error {
	slog.Info("Resetting scores and clearing Redis queues")

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
	se.RedisClient.Publish(context.Background(), "events", "reset")

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
