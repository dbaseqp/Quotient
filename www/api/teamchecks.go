package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"quotient/engine/db"
)

// GetTeamChecks returns per-team service check states for admins
func GetTeamChecks(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Collect unique services from configuration
	serviceMap := make(map[string]bool)
	for _, chk := range eng.Config.AllChecks() {
		serviceMap[chk.GetName()] = true
	}
	services := make([]string, 0, len(serviceMap))
	for s := range serviceMap {
		services = append(services, s)
	}

	type teamState struct {
		TeamID   uint            `json:"team_id"`
		TeamName string          `json:"team_name"`
		Services map[string]bool `json:"services"`
	}

	var states []teamState
	for _, team := range teams {
		serviceStates := make(map[string]bool)
		for service := range serviceMap {
			enabled, err := db.IsTeamServiceEnabled(team.ID, service)
			if err != nil {
				slog.Error("failed to get service state", "team", team.ID, "service", service, "error", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			serviceStates[service] = enabled
		}
		states = append(states, teamState{TeamID: team.ID, TeamName: team.Name, Services: serviceStates})
	}

	resp := map[string]any{"services": services, "states": states}
	WriteJSON(w, http.StatusOK, resp)
}

// UpdateTeamChecks updates per-team service check states
func UpdateTeamChecks(w http.ResponseWriter, r *http.Request) {
	type update struct {
		TeamID      uint   `json:"team_id"`
		ServiceName string `json:"service_name"`
		Enabled     bool   `json:"enabled"`
	}
	type form struct {
		Updates []update `json:"updates"`
	}
	var f form
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, u := range f.Updates {
		if err := db.SetTeamServiceEnabled(u.TeamID, u.ServiceName, u.Enabled); err != nil {
			slog.Error("failed to update service state", "team", u.TeamID, "service", u.ServiceName, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}
