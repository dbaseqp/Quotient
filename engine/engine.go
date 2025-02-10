package engine

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"quotient/engine/checks"
	"quotient/engine/config"
	"quotient/engine/db"

	"github.com/redis/go-redis/v9"
)

type Task struct {
	TeamID         uint            `json:"team_id"`         // Numeric identifier for the team
	TeamIdentifier string          `json:"team_identifier"` // Human-readable identifier for the team
	ServiceType    string          `json:"service_type"`
	ServiceName    string          `json:"service_name"`
	Deadline       time.Time       `json:"deadline"`
	RoundID        uint            `json:"round_id"`
	CheckData      json.RawMessage `json:"check_data"`
}

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

func (se *ScoringEngine) GetCredlists() (any, error) {
	var credlists []any
	for _, credlist := range se.Config.CredlistSettings.Credlist {
		var a struct {
			Name      string   `json:"name"`
			Path      string   `json:"path"`
			Usernames []string `json:"usernames"`
			Example   string   `json:"example"`
		}
		a.Name = credlist.CredlistName
		a.Example = credlist.CredlistExplainText
		a.Path = credlist.CredlistPath
		a.Usernames = []string{}
		credlistPath := filepath.Join("config/credlists", credlist.CredlistPath)
		file, err := os.Open(credlistPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open credlist file %s: %v", credlistPath, err)
		}
		defer file.Close()

		reader := csv.NewReader(file)
		records, err := reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("failed to read credlist file %s: %v", credlistPath, err)
		}

		for _, record := range records {
			if len(record) != 2 {
				return nil, fmt.Errorf("invalid credlist format")
			}
			a.Usernames = append(a.Usernames, record[0])
		}
		credlists = append(credlists, a)
	}
	return credlists, nil
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

func (se *ScoringEngine) LoadCredentials() error {
	credlistFiles, err := os.ReadDir("config/credlists/")
	if err != nil {
		return fmt.Errorf("failed to read credlists directory: %v", err)
	}

	// remove credlists not in the config
	// for i, credcredlistFiles := range credlistFiles {
	// 	for _, configCredlist := range se.Config.CredlistSettings.Credlist {
	// 		if credcredlistFiles.Name() == configCredlist.CredlistPath {
	// 			// assume the last element is OK, so replace the current element with it
	// 			// and then truncate the slice
	// 			credlistFiles[i] = credlistFiles[len(credlistFiles)-1]
	// 			credlistFiles = credlistFiles[:len(credlistFiles)-1]
	// 		}
	// 	}
	// }

	// remove credlists not in the config
	// for i, credcredlistFiles := range credlistFiles {
	// 	for _, configCredlist := range se.Config.CredlistSettings.Credlist {
	// 		if credcredlistFiles.Name() == configCredlist.CredlistPath {
	// 			// assume the last element is OK, so replace the current element with it
	// 			// and then truncate the slice
	// 			credlistFiles[i] = credlistFiles[len(credlistFiles)-1]
	// 			credlistFiles = credlistFiles[:len(credlistFiles)-1]
	// 		}
	// 	}
	// }

	teams, err := db.GetTeams()
	if err != nil {
		return fmt.Errorf("failed to get teams: %v", err)
	}

	for _, team := range teams {
		se.CredentialsMutex[team.ID] = &sync.Mutex{}
		for _, credlistFile := range credlistFiles {
			if !credlistFile.IsDir() && filepath.Ext(credlistFile.Name()) == ".credlist" {
				submissionPath := fmt.Sprintf("submissions/pcrs/%d/%s", team.ID, credlistFile.Name())
				if _, err := os.Stat(submissionPath); os.IsNotExist(err) {
					destDir := filepath.Dir(submissionPath)
					if err := os.MkdirAll(destDir, os.ModePerm); err != nil {
						return fmt.Errorf("failed to create directory %s: %v", destDir, err)
					}

					sourcePath := fmt.Sprintf("config/credlists/%s", credlistFile.Name())
					sourceFile, err := os.Open(sourcePath)
					if err != nil {
						return fmt.Errorf("failed to open source file %s: %v", sourcePath, err)
					}
					defer sourceFile.Close()

					destFile, err := os.Create(submissionPath)
					if err != nil {
						return fmt.Errorf("failed to create destination file %s: %v", submissionPath, err)
					}
					defer destFile.Close()

					if _, err := io.Copy(destFile, sourceFile); err != nil {
						return fmt.Errorf("failed to copy file from %s to %s: %v", sourcePath, submissionPath, err)
					}
				} else if err != nil {
					return fmt.Errorf("failed to check file %s: %v", submissionPath, err)
				}
			}
		}
	}

	return nil
}

func (se *ScoringEngine) UpdateCredentials(teamID uint, credlistName string, usernames []string, passwords []string) error {
	validCredlist := false
	for _, c := range se.Config.CredlistSettings.Credlist {
		if c.CredlistPath == credlistName {
			validCredlist = true
			break
		}
	}
	if !validCredlist {
		return fmt.Errorf("invalid credlist name")
	}

	se.CredentialsMutex[teamID].Lock()
	defer se.CredentialsMutex[teamID].Unlock()

	slog.Debug("updating credentials", "teamID", teamID, "credlistName", credlistName)

	if len(usernames) != len(passwords) {
		return fmt.Errorf("mismatched usernames and passwords")
	}

	credlistPath := fmt.Sprintf("submissions/pcrs/%d/%s", teamID, credlistName)
	originalCreds := make(map[string]string)
	credlist, err := os.Open(credlistPath)
	if err != nil {
		return fmt.Errorf("failed to read original credlist: %v", err)
	}

	reader := csv.NewReader(credlist)
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read original credlist: %v", err)
	}

	for _, record := range records {
		if len(record) != 2 {
			slog.Debug("invalid credlist format", "record", record)
			return fmt.Errorf("invalid credlist format")
		}
		originalCreds[record[0]] = record[1]
	}

	for i, username := range usernames {
		if _, exists := originalCreds[username]; !exists {
			slog.Debug("username not found in original credlist, skipping update", "username", username)
		} else {
			originalCreds[username] = passwords[i]
		}
	}

	credlist.Close()

	// write back to the file that was read
	credlistFile, err := os.Create(credlistPath)
	if err != nil {
		return fmt.Errorf("failed to open credlist file for writing: %v", err)
	}
	defer credlistFile.Close()

	writer := csv.NewWriter(credlistFile)
	for username, password := range originalCreds {
		// csv write encoded
		if err := writer.Write([]string{username, password}); err != nil {
			return fmt.Errorf("failed to write to credlist file: %v", err)
		}
		slog.Debug("successfully wrote to credlist", "username", username, "password", password)
	}
	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf("failed to flush pcr writer: %v", err)
	}

	return nil
}
