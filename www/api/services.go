package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"quotient/engine/db"
	"slices"
	"strconv"
	"strings"
)

func GetTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		username := r.Context().Value("username").(string)

		// Check if this is an OIDC user
		var teamToShow *db.TeamSchema
		var isOIDCUser bool
		if userInfo, exists := GetOIDCUserInfo(username); exists {
			isOIDCUser = true
			// Map OIDC user to team based on their groups
			teamToShow = mapOIDCUserToTeam(teams, userInfo.Groups)
		}

		// Fall back to username-based lookup only for non-OIDC users
		if !isOIDCUser && teamToShow == nil {
			me, err := db.GetTeamByUsername(username)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if me.ID != 0 {
				teamToShow = &me
			}
		}

		// Filter teams to show only the user's team
		if teamToShow != nil {
			for _, team := range teams {
				if team.ID == teamToShow.ID {
					teams = []db.TeamSchema{team}
					break
				}
			}
		} else {
			// No team found for user, return empty array
			teams = []db.TeamSchema{}
		}
	}

	d, _ := json.Marshal(teams)
	w.Write(d)
}

// mapOIDCUserToTeam maps an OIDC user to a team based on their groups
func mapOIDCUserToTeam(teams []db.TeamSchema, userGroups []string) *db.TeamSchema {
	// Extract team number from group names that match OIDCTeamGroups patterns
	// Assumes last two digits indicate team number (e.g., "WCComps_Quotient_Blue_Team01" -> team01)
	for _, group := range userGroups {
		// Check if this group matches any of the configured team group patterns
		for _, pattern := range conf.OIDCSettings.OIDCTeamGroups {
			// Simple wildcard matching (e.g., "WCComps_Quotient_Blue*")
			basePattern := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(group, basePattern) {
				// Extract last two digits from the group name
				if len(group) >= 2 {
					lastTwo := group[len(group)-2:]
					if num, err := strconv.Atoi(lastTwo); err == nil {
						// Look for matching team (e.g., 01 -> team01)
						teamName := fmt.Sprintf("team%02d", num)
						for i := range teams {
							if teams[i].Name == teamName {
								return &teams[i]
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// getUserTeamID returns the team ID for a given username, supporting both OIDC and local users
func getUserTeamID(username string) (uint, error) {
	// Check if this is an OIDC user
	if userInfo, exists := GetOIDCUserInfo(username); exists {
		teams, err := db.GetTeams()
		if err != nil {
			return 0, err
		}
		teamToShow := mapOIDCUserToTeam(teams, userInfo.Groups)
		if teamToShow != nil {
			return teamToShow.ID, nil
		}
		return 0, fmt.Errorf("OIDC user not mapped to any team")
	}

	// Fall back to username-based lookup for local users
	me, err := db.GetTeamByUsername(username)
	if err != nil {
		return 0, err
	}
	if me.ID == 0 {
		return 0, fmt.Errorf("user not associated with any team")
	}
	return me.ID, nil
}

func GetTeamSummary(w http.ResponseWriter, r *http.Request) {
	if !CheckCompetitionStarted(w, r) {
		return
	}

	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		myTeamID, err := getUserTeamID(r.Context().Value("username").(string))
		if err != nil {
			slog.Error("Failed to get user's team", "username", r.Context().Value("username").(string), "err", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if teamID != myTeamID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	summaries, err := db.GetTeamSummary(teamID)
	if err != nil {
		slog.Error("Failed to get team summary", "teamID", teamID, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	type summary struct {
		ServiceName  string           `json:"ServiceName"`
		SlaCount     int              `json:"SlaCount"`
		Last10Rounds []db.RoundSchema `json:"Last10Rounds"`
		Uptime       float64          `json:"Uptime"`
	}

	var s []summary
	for _, v := range summaries {
		uptime := eng.UptimePerService[teamID][v["ServiceName"].(string)]
		s = append(s, summary{
			ServiceName:  v["ServiceName"].(string),
			SlaCount:     v["SlaCount"].(int),
			Last10Rounds: v["Last10Rounds"].([]db.RoundSchema),
			Uptime:       float64(uptime.PassedChecks) / float64(uptime.TotalChecks),
		})
	}

	d, _ := json.Marshal(s)
	w.Write(d)
}

func GetServiceAll(w http.ResponseWriter, r *http.Request) {
	if !CheckCompetitionStarted(w, r) {
		return
	}

	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	serviceID := r.PathValue("service_name")

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		myTeamID, err := getUserTeamID(r.Context().Value("username").(string))
		if err != nil {
			slog.Error("Failed to get user's team", "username", r.Context().Value("username").(string), "err", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if teamID != myTeamID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	service, err := db.GetServiceAllChecksByTeam(teamID, serviceID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
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

	d, _ := json.Marshal(service)
	w.Write(d)
}

func CreateService(w http.ResponseWriter, r *http.Request) {

}

func UpdateService(w http.ResponseWriter, r *http.Request) {

}

func DeleteService(w http.ResponseWriter, r *http.Request) {

}
