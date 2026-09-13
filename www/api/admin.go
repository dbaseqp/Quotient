package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
)

var validIdentifierRegex = regexp.MustCompile(`^[0-9]{1,3}$`)

func isValidIdentifier(identifier string) bool {
	return validIdentifierRegex.MatchString(identifier)
}

func (a *API) PauseEngine(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Pause bool `json:"pause"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	slog.Debug("pause engine requested", "wasEnginePausedWhenRequestIssued", a.eng.IsEnginePaused, "setPauseTo", form.Pause)
	if a.eng.IsEnginePaused && !form.Pause {
		a.eng.ResumeEngine()
	} else if !a.eng.IsEnginePaused && form.Pause {
		a.eng.PauseEngine()
	} else {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid engine state transition"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) ResetScores(w http.ResponseWriter, r *http.Request) {
	slog.Debug("reset scores requested")
	if err := a.eng.ResetScores(); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to reset scores"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) SetCompetitionStarted(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Started bool `json:"started"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	slog.Info("competition started toggle requested", "started", form.Started)
	if err := a.eng.DB.SetCompetitionStarted(form.Started); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update competition status"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) ExportScores(w http.ResponseWriter, r *http.Request) {
	type ServiceScore struct {
		ServiceName   string `json:"service_name"`
		Points        int    `json:"service_points"`
		SlaViolations int    `json:"sla_violations"`
		SlaPenalty    int    `json:"sla_penalty"`
	}

	type TeamScore struct {
		TeamID             uint           `json:"team_id"`
		TeamName           string         `json:"team_name"`
		Services           []ServiceScore `json:"services"`
		TotalPoints        int            `json:"total_points"`
		GrossPoints        int            `json:"gross_points"`
		TotalSLAPenalty    int            `json:"total_sla_penalty"`
		TotalSLAViolations int            `json:"total_sla_violations"`
	}

	teams, err := a.eng.DB.GetTeams()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve teams"})
		return
	}

	teamScores := make(map[uint]*TeamScore)
	for _, team := range teams {
		teamScores[team.ID] = &TeamScore{
			TeamID:   team.ID,
			TeamName: team.Name,
			Services: []ServiceScore{},
		}
	}

	serviceData, err := a.eng.DB.GetServiceScores()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve service scores"})
		return
	}

	for _, sd := range serviceData {
		if team, ok := teamScores[sd.TeamID]; ok {
			service := ServiceScore{
				ServiceName:   sd.ServiceName,
				Points:        sd.Points,
				SlaViolations: sd.Violations,
				SlaPenalty:    sd.TotalPenalty,
			}
			team.Services = append(team.Services, service)
			team.TotalPoints += sd.Points - sd.TotalPenalty
			team.GrossPoints += sd.Points
			team.TotalSLAPenalty += sd.TotalPenalty
			team.TotalSLAViolations += sd.Violations
		}
	}

	var data []TeamScore
	for _, score := range teamScores {
		data = append(data, *score)
	}

	WriteJSON(w, http.StatusOK, data)
}

func (a *API) ExportConfig(w http.ResponseWriter, r *http.Request) {

}

func (a *API) GetActiveTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := a.eng.GetActiveTasks()
	if err != nil {
		slog.Error("failed to get active tasks", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve active tasks"})
		return
	}

	WriteJSON(w, http.StatusOK, tasks)
}

func (a *API) GetEngine(w http.ResponseWriter, r *http.Request) {
	lastRound, err := a.eng.DB.GetLastRound()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve engine status"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"last_round":          lastRound,
		"current_round_time":  a.eng.CurrentRoundStartTime,
		"next_round_time":     a.eng.NextRoundStartTime,
		"running":             !a.eng.IsEnginePaused,
		"competition_started": a.eng.DB.GetCompetitionStarted(),
	})
}

func (a *API) UpdateTeams(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Teams []struct {
			TeamID     uint   `json:"id"`
			Identifier string `json:"identifier"`
			Active     bool   `json:"active"`
		} `json:"teams"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	for _, team := range form.Teams {
		// Validate identifier format to prevent command injection
		if !isValidIdentifier(team.Identifier) {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid identifier format. Only alphanumeric, hyphens, and underscores allowed."})
			return
		}

		if err := a.eng.DB.UpdateTeam(uint(team.TeamID), team.Identifier, team.Active); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update team"})
			return
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}
