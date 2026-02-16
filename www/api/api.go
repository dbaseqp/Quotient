package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/engine/db"
)

var (
	conf *config.ConfigSettings
	eng  *engine.ScoringEngine
)

func SetConfig(c *config.ConfigSettings) {
	conf = c
}

func SetEngine(e *engine.ScoringEngine) {
	eng = e
}

// WriteJSON writes a JSON response with the given status code.
// Errors are logged but not returned since there's nothing actionable
// the caller can do if the response write fails.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}

// SafeOpen opens a file within the given base directory safely.
// It prevents directory traversal attacks using os.Root.
func SafeOpen(baseDir, relativePath string) (*os.File, error) {
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open root directory: %w", err)
	}
	defer root.Close()
	return root.Open(relativePath)
}

// SafeCreate creates a file within the given base directory safely.
func SafeCreate(baseDir, relativePath string) (*os.File, error) {
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open root directory: %w", err)
	}
	defer root.Close()
	return root.Create(relativePath)
}

// CheckCompetitionStarted returns false and writes error response if competition hasn't started
// Admins always have access regardless of competition start time
func CheckCompetitionStarted(w http.ResponseWriter, r *http.Request) bool {
	roles := r.Context().Value("roles")
	if roles != nil {
		roleList := roles.([]string)
		for _, role := range roleList {
			if role == "admin" {
				return true
			}
		}
	}

	if !db.GetCompetitionStarted() {
		WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Competition has not started"})
		return false
	}
	return true
}
