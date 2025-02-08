package api

import (
	"encoding/json"
	"net/http"
	"quotient/engine/db"
	"slices"
)

const (
	Unknown = iota
	Up
	Down
)

func GetServiceStatus(w http.ResponseWriter, r *http.Request) {
	round, err := db.GetLastRound()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]interface{}{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	uniqueServicesMap := make(map[string]bool)
	for _, check := range round.Checks {
		uniqueServicesMap[check.ServiceName] = true
	}

	uniqueServices := make([]string, 0, len(uniqueServicesMap))
	for service := range uniqueServicesMap {
		uniqueServices = append(uniqueServices, service)
	}

	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]interface{}{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	teams = slices.DeleteFunc(teams, func(team db.TeamSchema) bool { return !team.Active })

	type Point struct {
		X string
		Y int
	}

	type Series struct {
		Name string
		Data []Point
	}
	temp := make(map[string]map[string]Point)
	for _, team := range teams {
		temp[team.Name] = make(map[string]Point)
		for _, uniqueName := range uniqueServices {
			temp[team.Name][uniqueName] = Point{X: uniqueName, Y: Unknown}
		}
		for _, check := range round.Checks {
			if team.ID == check.TeamID {
				if check.Result {
					temp[team.Name][check.ServiceName] = Point{X: check.ServiceName, Y: Up}
				} else {
					temp[team.Name][check.ServiceName] = Point{X: check.ServiceName, Y: Down}
				}
			}
		}
	}

	var series []Series
	for teamName, teamSeries := range temp {
		s := Series{Name: teamName}

		var points []Point
		for _, point := range teamSeries {
			points = append(points, point)
		}

		s.Data = points
		series = append(series, s)
	}

	data := map[string]interface{}{"series": series}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func GetScoreStatus(w http.ResponseWriter, r *http.Request) {
	scores, err := db.GetServiceCheckSumByRound()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]interface{}{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	type Point struct {
		Round int
		Total int
	}
	type Series struct {
		Name string
		Data []Point
	}

	series := make([]Series, 0, len(scores))

	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]interface{}{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	teams = slices.DeleteFunc(teams, func(team db.TeamSchema) bool { return !team.Active })

	for _, team := range teams {
		s := Series{Name: team.Name}
		series = append(series, s)
	}

	for round, allTeamsThisRound := range scores {
		for team_id := range allTeamsThisRound {
			for i, team := range teams {
				if team.ID == team_id {
					series[i].Data = append(series[i].Data, Point{Round: round + 1, Total: allTeamsThisRound[team_id]})
				}
			}
		}
	}

	if r.Context().Value("roles") != nil {
		req_roles := r.Context().Value("roles").([]string)
		if !slices.Contains(req_roles, "admin") {
			for i, _ := range series {
				series[i].Name = "Team"
			}
		}
	} else {
		for i, _ := range series {
			series[i].Name = "Team"
		}
	}

	// Sort the series by the highest Total in the last Point in Data
	slices.SortFunc(series, func(a, b Series) int {
		if len(a.Data) == 0 {
			return 1
		}
		if len(b.Data) == 0 {
			return -1
		}
		return b.Data[len(b.Data)-1].Total - a.Data[len(a.Data)-1].Total
	})

	data := map[string]interface{}{"series": series}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func GetUptimeStatus(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]interface{}{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	teams = slices.DeleteFunc(teams, func(team db.TeamSchema) bool { return !team.Active })

	uptime := eng.GetUptimePerService()

	// TODO: make db unique function or get from config
	uniqueServicesMap := make(map[string]bool)
	for _, team := range teams {
		for service := range uptime[team.ID] {
			uniqueServicesMap[service] = true
		}
	}
	uniqueServices := make([]string, 0)
	for service := range uniqueServicesMap {
		uniqueServices = append(uniqueServices, service)
	}

	type Point struct {
		Service string
		Uptime  float64
	}
	type Series struct {
		Name string
		Data []Point
	}

	series := make([]Series, 0, len(uptime))
	for _, team := range teams {
		s := Series{Name: team.Name}

		var points []Point
		for _, servicename := range uniqueServices {
			percentage := -0.01
			for service, uptime := range uptime[team.ID] {
				if service == servicename {
					percentage = float64(uptime.PassedChecks) / float64(uptime.TotalChecks)
				}
			}
			points = append(points, Point{Service: servicename, Uptime: percentage})
		}
		s.Data = points
		series = append(series, s)
	}

	if r.Context().Value("roles") != nil {
		req_roles := r.Context().Value("roles").([]string)
		if !slices.Contains(req_roles, "admin") {
			for i, _ := range series {
				series[i].Name = "Team"
			}
		}
	} else {
		for i, _ := range series {
			series[i].Name = "Team"
		}
	}

	data := map[string]interface{}{"series": series}
	d, _ := json.Marshal(data)
	w.Write(d)
}
