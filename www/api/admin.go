package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"quotient/engine/db"
	"regexp"
)

var validIdentifierRegex = regexp.MustCompile(`^[0-9]{1,3}$`)

func isValidIdentifier(identifier string) bool {
	return validIdentifierRegex.MatchString(identifier)
}

func PauseEngine(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Pause bool `json:"pause"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	slog.Debug("pause engine requested", "wasEnginePausedWhenRequestIssued", eng.IsEnginePaused, "setPauseTo", form.Pause)
	if eng.IsEnginePaused && !form.Pause {
		eng.ResumeEngine()
	} else if !eng.IsEnginePaused && form.Pause {
		eng.PauseEngine()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	d := []byte(`{"status": "success"}`)
	w.Write(d)
}

func ResetScores(w http.ResponseWriter, r *http.Request) {
	slog.Debug("reset scores requested")
	if err := eng.ResetScores(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	d := []byte(`{"status": "success"}`)
	w.Write(d)
}

func SetCompetitionStarted(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Started bool `json:"started"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	slog.Info("competition started toggle requested", "started", form.Started)
	if err := db.SetCompetitionStarted(form.Started); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	d := []byte(`{"status": "success"}`)
	w.Write(d)
}

func ExportScores(w http.ResponseWriter, r *http.Request) {
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

	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
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

	serviceData, err := db.GetServiceScores()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
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

	d, _ := json.Marshal(data)
	w.Write(d)
}

func ExportConfig(w http.ResponseWriter, r *http.Request) {

}

func GetActiveTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := eng.GetActiveTasks()
	if err != nil {
		slog.Error("failed to get active tasks", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	d, _ := json.Marshal(tasks)
	w.Write(d)
}

func GetEngine(w http.ResponseWriter, r *http.Request) {
	lastRound, err := db.GetLastRound()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	d, _ := json.Marshal(map[string]any{
		"last_round":          lastRound,
		"current_round_time":  eng.CurrentRoundStartTime,
		"next_round_time":     eng.NextRoundStartTime,
		"running":             !eng.IsEnginePaused,
		"competition_started": db.GetCompetitionStarted(),
	})
	w.Write(d)
}

func UpdateTeams(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Teams []struct {
			TeamID     int    `json:"id"`
			Identifier string `json:"identifier"`
			Active     bool   `json:"active"`
		} `json:"teams"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, team := range form.Teams {
		// Validate identifier format to prevent command injection
		if !isValidIdentifier(team.Identifier) {
			w.WriteHeader(http.StatusBadRequest)
			d, _ := json.Marshal(map[string]string{"error": "Invalid identifier format. Only alphanumeric, hyphens, and underscores allowed."})
			w.Write(d)
			return
		}

		if err := db.UpdateTeam(uint(team.TeamID), team.Identifier, team.Active); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	d := []byte(`{"status": "success"}`)
	w.Write(d)
}
