package api

import (
	"log/slog"
	"net/http"
	"quotient/engine/db"
	"slices"
	"strconv"
)

func GetTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving teams"})
		return
	}
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		myTeamID, hasTeam := CallerTeamID(r.Context())
		teams = filterToTeam(teams, myTeamID, hasTeam)
	}

	WriteJSON(w, http.StatusOK, teams)
}

// filterToTeam narrows a team list to the caller's own team.
func filterToTeam(teams []db.TeamSchema, teamID uint, hasTeam bool) []db.TeamSchema {
	if !hasTeam {
		return []db.TeamSchema{}
	}
	for _, team := range teams {
		if team.ID == teamID {
			return []db.TeamSchema{team}
		}
	}
	return []db.TeamSchema{}
}

func GetTeamSummary(w http.ResponseWriter, r *http.Request) {
	if !CheckCompetitionStarted(w, r) {
		return
	}

	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return
	}
	teamID := uint(temp)

	if !requireOwnTeam(w, r, teamID, "admin") {
		return
	}

	summaries, err := db.GetTeamSummary(teamID)
	if err != nil {
		slog.Error("Failed to get team summary", "teamID", teamID, "err", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving team summary"})
		return
	}

	type summary struct {
		ServiceName  string           `json:"ServiceName"`
		SlaCount     int              `json:"SlaCount"`
		Last10Rounds []db.RoundSchema `json:"Last10Rounds"`
		Uptime       float64          `json:"Uptime"`
	}

	eng.RLockUptime()
	uptimeMap := eng.GetUptimePerService()
	var s []summary
	for _, v := range summaries {
		uptime := uptimeMap[teamID][v["ServiceName"].(string)]
		s = append(s, summary{
			ServiceName:  v["ServiceName"].(string),
			SlaCount:     v["SlaCount"].(int),
			Last10Rounds: v["Last10Rounds"].([]db.RoundSchema),
			Uptime:       float64(uptime.PassedChecks) / float64(uptime.TotalChecks),
		})
	}
	eng.RUnlockUptime()

	WriteJSON(w, http.StatusOK, s)
}

func GetServiceAll(w http.ResponseWriter, r *http.Request) {
	if !CheckCompetitionStarted(w, r) {
		return
	}

	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return
	}
	teamID := uint(temp)

	serviceID := r.PathValue("service_name")
	req_roles := r.Context().Value("roles").([]string)

	if !requireOwnTeam(w, r, teamID, "admin") {
		return
	}

	service, err := db.GetServiceAllChecksByTeam(teamID, serviceID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving service data"})
		return
	}

	// Remove debug and error fields for non-admins
	// Red team never sees credentials, blue team only if ShowDebugToBlueTeam is enabled
	if !slices.Contains(req_roles, "admin") && (slices.Contains(req_roles, "red") || !conf.MiscSettings.ShowDebugToBlueTeam) {
		for i := range service {
			service[i].Debug = ""
			service[i].Error = ""
		}
	}

	WriteJSON(w, http.StatusOK, service)
}

func CreateService(w http.ResponseWriter, r *http.Request) {

}

func UpdateService(w http.ResponseWriter, r *http.Request) {

}

func DeleteService(w http.ResponseWriter, r *http.Request) {

}
