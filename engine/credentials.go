package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"quotient/engine/db"
	"sync"
)

func (se *ScoringEngine) LoadCredentials() error {
	credlistFiles, err := os.ReadDir("config/credlists/")
	if err != nil {
		return fmt.Errorf("failed to read credlists directory: %v", err)
	}

	// remove credlists not in the config
	for i, credcredlistFiles := range credlistFiles {
		for _, configCredlist := range se.Config.CredlistSettings.Credlist {
			if credcredlistFiles.Name() == configCredlist.CredlistPath {
				// assume the last element is OK, so replace the current element with it
				// and then truncate the slice
				credlistFiles[i] = credlistFiles[len(credlistFiles)-1]
				credlistFiles = credlistFiles[:len(credlistFiles)-1]
			}
		}
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

func (se *ScoringEngine) UpdateCredentials(teamID uint, credlistName string, usernames []string, passwords []string) (int, error) {
	// check if the credlist name is in the config
	validCredlist := false
	for _, c := range se.Config.CredlistSettings.Credlist {
		if c.CredlistPath == credlistName {
			validCredlist = true
			break
		}
	}
	if !validCredlist {
		return 0, fmt.Errorf("invalid credlist name")
	}

	se.CredentialsMutex[teamID].Lock()
	defer se.CredentialsMutex[teamID].Unlock()

	slog.Debug("updating credentials", "teamID", teamID, "credlistName", credlistName)

	if len(usernames) != len(passwords) {
		return 0, fmt.Errorf("mismatched usernames and passwords")
	}

	credlistPath := fmt.Sprintf("submissions/pcrs/%d/%s", teamID, credlistName)
	originalCreds := make(map[string]string)
	credlist, err := os.Open(credlistPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read original credlist: %v", err)
	}

	reader := csv.NewReader(credlist)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("failed to read original credlist: %v", err)
	}

	for _, record := range records {
		if len(record) != 2 {
			slog.Debug("invalid credlist format", "record", record)
			return 0, fmt.Errorf("invalid credlist format")
		}
		originalCreds[record[0]] = record[1]
	}

	updatedCount := 0
	for i, username := range usernames {
		if _, exists := originalCreds[username]; !exists {
			slog.Debug("username not found in original credlist, skipping update", "username", username)
		} else {
			originalCreds[username] = passwords[i]
			updatedCount++
		}
	}

	credlist.Close()

	// write back to the file that was read
	credlistFile, err := os.Create(credlistPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open credlist file for writing: %v", err)
	}
	defer credlistFile.Close()

	writer := csv.NewWriter(credlistFile)
	for username, password := range originalCreds {
		// csv write encoded
		if err := writer.Write([]string{username, password}); err != nil {
			return 0, fmt.Errorf("failed to write to credlist file: %v", err)
		}
		slog.Debug("successfully wrote to credlist", "username", username, "password", password)
	}
	writer.Flush()

	if err := writer.Error(); err != nil {
		return 0, fmt.Errorf("failed to flush pcr writer: %v", err)
	}

	return updatedCount, nil
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
