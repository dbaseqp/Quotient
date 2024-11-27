package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"quotient/engine/checks"
	"quotient/engine/config"
	"quotient/engine/db"
)

type ScoringEngine struct {
	Config                *config.ConfigSettings
	CredentialsMutex      map[uint]*sync.Mutex
	UptimePerService      map[uint]map[string]db.Uptime
	SlaPerService         map[uint]map[string]int
	EnginePauseWg         *sync.WaitGroup
	IsEnginePaused        bool
	CurrentRound          int
	NextRoundStartTime    time.Time
	CurrentRoundStartTime time.Time

	// signals
	ResetChan chan struct{}
}

func NewEngine(conf *config.ConfigSettings) *ScoringEngine {
	return &ScoringEngine{
		Config:           conf,
		CredentialsMutex: make(map[uint]*sync.Mutex),
		UptimePerService: make(map[uint]map[string]db.Uptime),
		SlaPerService:    make(map[uint]map[string]int),
		ResetChan:        make(chan struct{}),
	}
}

func (se *ScoringEngine) Start() {
	if t, err := db.GetLastRound(); err != nil {
		log.Fatalf("failed to get last round: %v", err)
	} else {
		se.CurrentRound = int(t.ID) + 1
	}

	if err := db.LoadUptimes(&se.UptimePerService); err != nil {
		log.Fatalf("failed to load uptimes: %v", err)
	}

	if err := db.LoadSLAs(&se.SlaPerService, se.Config.MiscSettings.SlaThreshold); err != nil {
		log.Fatalf("failed to load SLAs: %v", err)
	}

	// load credentials
	err := se.LoadCredentials()
	if err != nil {
		log.Fatalf("failed to load credential files into teams: %v", err)
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
				log.Fatalf("Unknown event type: %s", se.Config.RequiredSettings.EventType)
			}

			slog.Info(fmt.Sprintf("Round %d complete", se.CurrentRound))
			se.CurrentRound++

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

func (se *ScoringEngine) GetCredlists() []string {
	var credlists []string

	credlistFiles, err := os.ReadDir("config/credlists/")
	if err != nil {
		slog.Error("failed to read credlists directory:", "error", err)
		return nil
	}

	for _, credlistFile := range credlistFiles {
		if !credlistFile.IsDir() && filepath.Ext(credlistFile.Name()) == ".credlist" {
			credlists = append(credlists, credlistFile.Name())
		}
	}

	return credlists
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
	slog.Info("resetting scores")
	se.ResetChan <- struct{}{}
	if err := db.ResetScores(); err != nil {
		slog.Error("failed to reset scores", "error", err)
		return fmt.Errorf("failed to reset scores: %v", err)
	}
	se.CurrentRound = 1
	se.UptimePerService = make(map[uint]map[string]db.Uptime)
	se.SlaPerService = make(map[uint]map[string]int)
	slog.Info("scores reset successfully")
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
	resultsChan := make(chan checks.Result)
	results := make([]db.ServiceCheckSchema, 0)

	slog.Debug("Starting service checks...")
	for _, box := range se.Config.Box {
		for _, runner := range box.Runners {
			if runner.Runnable() {
				for _, team := range teams {
					if team.Active {
						go runner.Run(team.ID, team.Identifier, resultsChan)
						runners++
					}
				}
			}
		}
	}

	round := db.RoundSchema{
		ID:        uint(se.CurrentRound),
		StartTime: se.CurrentRoundStartTime,
	}
	for runners > 0 {
		result := <-resultsChan
		results = append(results, db.ServiceCheckSchema{
			TeamID:      result.TeamID,
			RoundID:     uint(se.CurrentRound),
			ServiceName: result.ServiceName,
			Points:      result.Points,
			Result:      result.Status,
			Error:       result.Error,
			Debug:       result.Debug,
		})
		slog.Debug("service check finished", "team_id", result.TeamID, "service_name", result.ServiceName, "result", result.Status)
		runners--
	}

	round.Checks = results
	slog.Debug("finished all service checks")
	if _, err := db.CreateRound(round); err != nil {
		slog.Error("failed to create round", "error", err.Error())
	} else {
		for _, check := range results {
			// make sure uptime map is initialized
			if _, ok := se.UptimePerService[check.TeamID]; !ok {
				se.UptimePerService[check.TeamID] = make(map[string]db.Uptime)
			}
			if _, ok := se.UptimePerService[check.TeamID][check.ServiceName]; !ok {
				se.UptimePerService[check.TeamID][check.ServiceName] = db.Uptime{}
			}
			newUptime := se.UptimePerService[check.TeamID][check.ServiceName]
			if check.Result {
				newUptime.PassedChecks++
			}
			newUptime.TotalChecks++
			se.UptimePerService[check.TeamID][check.ServiceName] = newUptime

			// make sure sla map is initialized
			if _, ok := se.SlaPerService[check.TeamID]; !ok {
				se.SlaPerService[check.TeamID] = make(map[string]int)
			}
			if _, ok := se.SlaPerService[check.TeamID][check.ServiceName]; !ok {
				se.SlaPerService[check.TeamID][check.ServiceName] = 0
			}
			if check.Result {
				se.SlaPerService[check.TeamID][check.ServiceName] = 0
			} else {
				se.SlaPerService[check.TeamID][check.ServiceName]++
				if se.SlaPerService[check.TeamID][check.ServiceName] >= se.Config.MiscSettings.SlaThreshold {
					sla := db.SLASchema{
						TeamID:      check.TeamID,
						ServiceName: check.ServiceName,
						RoundID:     uint(se.CurrentRound),
						Penalty:     se.Config.MiscSettings.SlaPenalty,
					}
					db.CreateSLA(sla)

					se.SlaPerService[check.TeamID][check.ServiceName] = 0
				}
			}
		}
	}
}

func (se *ScoringEngine) LoadCredentials() error {
	credlistFiles, err := os.ReadDir("config/credlists/")
	if err != nil {
		return fmt.Errorf("failed to read credlists directory: %v", err)
	}

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
	se.CredentialsMutex[teamID].Lock()

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

	se.CredentialsMutex[teamID].Unlock()

	return nil
}
