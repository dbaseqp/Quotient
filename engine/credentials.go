package engine

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"quotient/engine/db"
	"sync"
)

// safeOpenInDir opens a file within the given base directory safely using os.Root.
func safeOpenInDir(baseDir, relativePath string) (*os.File, error) {
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open root directory: %w", err)
	}
	defer root.Close()
	return root.Open(relativePath)
}

func (se *ScoringEngine) EnsureCredentialsSeeded() error {
	teams, err := db.GetTeams()
	if err != nil {
		return fmt.Errorf("failed to get teams: %v", err)
	}

	// Initialize mutex map
	for _, team := range teams {
		se.CredentialsMutex[team.ID] = &sync.Mutex{}
	}

	// Check if credentials are already seeded in DB
	seeded, err := db.IsCredentialsSeeded()
	if err != nil {
		return fmt.Errorf("failed to check if credentials are seeded: %v", err)
	}

	if !seeded {
		slog.Info("Seeding credentials from config files to database")
		// Seed original credentials from files
		for _, configCredlist := range se.Config.CredlistSettings.Credlist {
			credlistPath := configCredlist.CredlistPath

			file, err := safeOpenInDir("config/credlists", credlistPath)
			if err != nil {
				return fmt.Errorf("failed to open credlist file %s: %v", credlistPath, err)
			}

			reader := csv.NewReader(file)
			records, err := reader.ReadAll()
			if closeErr := file.Close(); closeErr != nil {
				slog.Error("failed to close credlist file", "path", credlistPath, "error", closeErr)
			}
			if err != nil {
				return fmt.Errorf("failed to read credlist file %s: %v", credlistPath, err)
			}

			if err := db.SeedOriginalCredentials(credlistPath, records); err != nil {
				return fmt.Errorf("failed to seed original credentials for %s: %v", credlistPath, err)
			}
		}

		// Seed team credentials from originals
		for _, team := range teams {
			if err := db.SeedTeamCredentials(team.ID); err != nil {
				return fmt.Errorf("failed to seed credentials for team %d: %v", team.ID, err)
			}
		}
		slog.Info("Credentials seeded successfully")
	} else {
		slog.Info("Credentials already seeded in database")
		// Check for new teams that need seeding
		for _, team := range teams {
			creds, err := db.GetAllTeamCredentials(team.ID)
			if err != nil {
				return fmt.Errorf("failed to get credentials for team %d: %v", team.ID, err)
			}
			if len(creds) == 0 {
				slog.Info("Seeding credentials for new team", "team_id", team.ID)
				if err := db.SeedTeamCredentials(team.ID); err != nil {
					return fmt.Errorf("failed to seed credentials for team %d: %v", team.ID, err)
				}
			}
		}

		// Check for new credlists that need seeding
		for _, configCredlist := range se.Config.CredlistSettings.Credlist {
			credlistPath := configCredlist.CredlistPath
			origCreds, err := db.GetOriginalCredentials(credlistPath)
			if err != nil {
				return fmt.Errorf("failed to check original credentials for %s: %v", credlistPath, err)
			}
			if len(origCreds) == 0 {
				slog.Info("Seeding new credlist", "credlist", credlistPath)
				file, err := safeOpenInDir("config/credlists", credlistPath)
				if err != nil {
					return fmt.Errorf("failed to open credlist file %s: %v", credlistPath, err)
				}

				reader := csv.NewReader(file)
				records, err := reader.ReadAll()
				if closeErr := file.Close(); closeErr != nil {
					slog.Error("failed to close credlist file", "path", credlistPath, "error", closeErr)
				}
				if err != nil {
					return fmt.Errorf("failed to read credlist file %s: %v", credlistPath, err)
				}

				if err := db.SeedOriginalCredentials(credlistPath, records); err != nil {
					return fmt.Errorf("failed to seed original credentials for %s: %v", credlistPath, err)
				}

				// Seed to all teams
				for _, team := range teams {
					if err := db.SeedTeamCredentials(team.ID); err != nil {
						return fmt.Errorf("failed to seed credentials for team %d: %v", team.ID, err)
					}
				}
			}
		}
	}

	return nil
}

func (se *ScoringEngine) UpdateCredentials(teamID uint, credlistName string, usernames []string, passwords []string) (int, []string, error) {
	// Validate credlist name
	validCredlist := false
	for _, c := range se.Config.CredlistSettings.Credlist {
		if c.CredlistPath == credlistName {
			validCredlist = true
			break
		}
	}
	if !validCredlist {
		return 0, nil, fmt.Errorf("invalid credlist name")
	}

	se.CredentialsMutex[teamID].Lock()
	defer se.CredentialsMutex[teamID].Unlock()

	slog.Debug("updating credentials", "teamID", teamID, "credlistName", credlistName)

	if len(usernames) != len(passwords) {
		return 0, nil, fmt.Errorf("mismatched usernames and passwords")
	}

	updatedCount := 0
	var skippedUsernames []string
	changedBy := fmt.Sprintf("team%d", teamID)

	for i, username := range usernames {
		err := db.UpdateCredential(teamID, credlistName, username, passwords[i], changedBy)
		if err != nil {
			if err.Error() == "credential not found" {
				skippedUsernames = append(skippedUsernames, username)
				continue
			}
			return updatedCount, skippedUsernames, err
		}
		updatedCount++
	}

	return updatedCount, skippedUsernames, nil
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

		// Get usernames from original credentials in DB
		origCreds, err := db.GetOriginalCredentials(credlist.CredlistPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get original credentials for %s: %v", credlist.CredlistPath, err)
		}

		for _, cred := range origCreds {
			a.Usernames = append(a.Usernames, cred.Username)
		}
		credlists = append(credlists, a)
	}
	return credlists, nil
}

func (se *ScoringEngine) ResetCredentials(teamID uint, credlistName string, changedBy string) error {
	// Validate credlist name
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

	return db.ResetTeamCredlist(teamID, credlistName, changedBy)
}

// GetTeamCredentials returns credentials for admin viewing
func (se *ScoringEngine) GetTeamCredentials(teamID uint, credlistName string) ([]db.CredentialSchema, error) {
	return db.GetTeamCredentials(teamID, credlistName)
}

// GetPCRHistory returns PCR history for admin viewing
func (se *ScoringEngine) GetPCRHistory(teamID uint, credlistName string, username string) ([]db.PCRHistorySchema, error) {
	return db.GetPCRHistory(teamID, credlistName, username)
}
