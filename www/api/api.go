package api

import (
	"encoding/json"
	"net/http"
	"quotient/engine"
	"quotient/engine/config"
	"strings"
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

// SanitizedPath returns if the path is within the base directory
// It ensures that the path is within the base directory
func PathIsInDir(base string, relative string) bool {
	return strings.HasPrefix(relative, base)
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

	if !conf.HasCompetitionStarted() {
		w.WriteHeader(http.StatusForbidden)
		d, _ := json.Marshal(map[string]string{"error": "Competition has not started"})
		w.Write(d)
		return false
	}
	return true
}
