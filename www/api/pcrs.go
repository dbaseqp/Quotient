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
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR self service not allowed"})
		return
	}

	credlists, err := eng.GetCredlists()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error getting credlists"})
		slog.Error("Error getting credlists", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, credlists)
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
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		slog.Error("Failed to decode PCR json", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		if conf.MiscSettings.EasyPCR {
			me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
			if err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error looking up team"})
				return
			}
			if form.TeamID != fmt.Sprint(me.ID) {
				WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR not allowed"})
				return
			}
		} else {
			WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR not allowed"})
			return
		}
	}

	id, err := strconv.ParseUint(form.TeamID, 10, 64)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return
	}
	updatedCount, err := eng.UpdateCredentials(uint(id), form.CredlistPath, form.Usernames, form.Passwords)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error updating PCR"})
		slog.Error("Error updating PCR", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	data := map[string]any{
		"message": "PCR updated successfully",
		"count":   updatedCount,
	}
	WriteJSON(w, http.StatusOK, data)
}

func ResetPcr(w http.ResponseWriter, r *http.Request) {
	// get teamid from request
	// somehow determine which credlist to change
	type Form struct {
		TeamID       string `json:"team_id"`
		CredlistPath string `json:"credlist_id"`
	}

	var form Form
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&form)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		slog.Error("Failed to decode PCR json", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		if conf.MiscSettings.EasyPCR {
			me, err := db.GetTeamByUsername(r.Context().Value("username").(string))
			if err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error looking up team"})
				return
			}
			if form.TeamID != fmt.Sprint(me.ID) {
				WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR not allowed"})
				return
			}
		} else {
			WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR reset not allowed"})
			return
		}
	}

	id, err := strconv.ParseUint(form.TeamID, 10, 64)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return
	}
	if err := eng.ResetCredentials(uint(id), form.CredlistPath); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error resetting PCR"})
		slog.Error("Error resetting PCR", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	data := map[string]any{
		"message": "PCR reset successfully",
	}
	WriteJSON(w, http.StatusOK, data)
}
