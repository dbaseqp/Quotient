package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"quotient/engine/db"
	"slices"
	"strconv"
)

func GetTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
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

func GetTeamSummary(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
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

	summaries, err := db.GetTeamSummary(teamID)
	if err != nil {
		slog.Error("Failed to get team summary", "teamid", teamID, "err", err)
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
	temp, err := strconv.ParseUint(r.PathValue("team_id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	teamID := uint(temp)

	serviceID := r.PathValue("service_name")

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
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
	if !slices.Contains(req_roles, "admin") && !conf.MiscSettings.ShowDebugToBlueTeam {
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
