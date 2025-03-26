package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"quotient/engine/db"
	"slices"
	"strconv"
)

func GetCredlists(w http.ResponseWriter, r *http.Request) {
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && !conf.MiscSettings.EasyPCR {
		w.WriteHeader(http.StatusForbidden)
		data := map[string]any{"error": "PCR self service not allowed"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	credlists, err := eng.GetCredlists()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error getting credlists"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	d, _ := json.Marshal(credlists)
	w.Write(d)
}

func GetPcrs(w http.ResponseWriter, r *http.Request) {

}

func CreatePcr(w http.ResponseWriter, r *http.Request) {
	// get teamid from request
	// get username,password from request
	// somehow determine which credlist to change
	type Form struct {
		TeamID       string   `json:"team_id"`
		CredlistPath string   `json:"credlist_id"`
		Usernames    []string `json:"usernames"`
		Passwords    []string `json:"passwords"`
	}

	var form Form

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&form)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		if conf.MiscSettings.EasyPCR {
			me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if form.TeamID != fmt.Sprint(me.ID) {
				w.WriteHeader(http.StatusForbidden)
				data := map[string]any{"error": "PCR not allowed"}
				d, _ := json.Marshal(data)
				w.Write(d)
				return
			}
		} else {
			w.WriteHeader(http.StatusForbidden)
			data := map[string]any{"error": "PCR not allowed"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}
	}

	id, err := strconv.Atoi(form.TeamID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	updatedCount, err := eng.UpdateCredentials(uint(id), form.CredlistPath, form.Usernames, form.Passwords)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error updating PCR"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	data := map[string]any{
		"message": "PCR updated successfully",
		"count":   updatedCount,
	}
	d, _ := json.Marshal(data)
	w.Write(d)
}
