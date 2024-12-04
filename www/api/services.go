package api

import (
	"encoding/json"
	"net/http"
	"quotient/engine/db"
	"slices"
	"strconv"
)

/*
GetTeams handles the HTTP request to retrieve a list of teams.
If the user is not an admin, it restricts the response to the user's own team.
*/
func GetTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	reqRoles := r.Context().Value("roles").([]string)
	if !slices.Contains(reqRoles, "admin") {
		me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, team := range teams {
			if team.ID == me.ID {
				teams = []db.TeamSchema{team}
				break
			}
		}
	}

	d, _ := json.Marshal(teams)
	w.Write(d)
}

/*
GetTeamSummary handles the HTTP request to retrieve a summary of a specific team.
It checks the user's roles and permissions to ensure they are authorized to access the requested team's data.
*/
func GetTeamSummary(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	reqRoles := r.Context().Value("roles").([]string)
	if !slices.Contains(reqRoles, "admin") {
		me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if teamID != me.ID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	namePerService, slaCountPerService, last10RoundsPerService, err := db.GetTeamSummary(teamID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	d, _ := json.Marshal(map[string]interface{}{
		"service_names":  namePerService,
		"uptimes":        eng.GetUptimePerService()[teamID],
		"sla_counts":     slaCountPerService,
		"last_10_rounds": last10RoundsPerService,
	})
	w.Write(d)
}

/*
GetServiceAll handles the HTTP request to retrieve all service checks for a specific team and service.
It ensures the user has the appropriate permissions and optionally hides debug and error fields for non-admin users.
*/
func GetServiceAll(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	serviceID := r.PathValue("service_name")

	reqRoles := r.Context().Value("roles").([]string)
	if !slices.Contains(reqRoles, "admin") {
		me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if teamID != me.ID {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

	service, err := db.GetServiceAllChecksByTeam(teamID, serviceID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// if not admin and verbose is not set, remove the debug and error field
	if !slices.Contains(reqRoles, "admin") && conf.MiscSettings.ShowDebugToBlueTeam == false {
		for i := range service {
			service[i].Debug = ""
			service[i].Error = ""
		}
	}

	d, _ := json.Marshal(service)
	w.Write(d)
}

/*
CreateService handles the creation of a new service.
This function is currently a placeholder and needs implementation.
*/
func CreateService(w http.ResponseWriter, r *http.Request) {

}

/*
UpdateService handles the update of an existing service.
This function is currently a placeholder and needs implementation.
*/
func UpdateService(w http.ResponseWriter, r *http.Request) {

}

/*
DeleteService handles the deletion of an existing service.
This function is currently a placeholder and needs implementation.
*/
func DeleteService(w http.ResponseWriter, r *http.Request) {

}
