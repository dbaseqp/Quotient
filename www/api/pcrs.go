package api

import (
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strconv"

	"github.com/dbaseqp/Quotient/engine/db"
)

// requireJSONContentType blocks cross-origin simple requests (CSRF) by forcing a CORS preflight.
func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		WriteJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"})
		return false
	}
	return true
}

func (a *API) GetCredlists(w http.ResponseWriter, r *http.Request) {
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && !a.conf.MiscSettings.EasyPCR {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR self service not allowed"})
		return
	}

	credlists, err := a.eng.GetCredlists()
	if err != nil {
		WriteInternalError(w, r, "Error getting credlists", err)
		return
	}

	WriteJSON(w, http.StatusOK, credlists)
}

func (a *API) GetPcrs(w http.ResponseWriter, r *http.Request) {
	reqRoles, _ := r.Context().Value("roles").([]string)
	if !slices.Contains(reqRoles, "admin") {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
		return
	}

	// Get query parameters
	teamIDStr := r.URL.Query().Get("team_id")
	credlistName := r.URL.Query().Get("credlist")

	if teamIDStr == "" || credlistName == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "team_id and credlist are required"})
		return
	}

	teamID, err := strconv.ParseUint(teamIDStr, 10, 64)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team_id"})
		return
	}

	credentials, err := a.eng.GetTeamCredentials(uint(teamID), credlistName)
	if err != nil {
		WriteInternalError(w, r, "Error getting credentials", err)
		return
	}

	WriteJSON(w, http.StatusOK, credentials)
}

func (a *API) GetPcrHistory(w http.ResponseWriter, r *http.Request) {
	reqRoles, _ := r.Context().Value("roles").([]string)
	if !slices.Contains(reqRoles, "admin") {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
		return
	}

	// Get query parameters
	teamIDStr := r.URL.Query().Get("team_id")
	credlistName := r.URL.Query().Get("credlist")
	username := r.URL.Query().Get("username")

	if teamIDStr == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "team_id is required"})
		return
	}

	teamID, err := strconv.ParseUint(teamIDStr, 10, 64)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team_id"})
		return
	}

	history, err := a.eng.GetPCRHistory(uint(teamID), credlistName, username)
	if err != nil {
		WriteInternalError(w, r, "Error getting PCR history", err)
		return
	}

	WriteJSON(w, http.StatusOK, history)
}

// allowPCRForTeam reports whether the caller may act on teamID, writing the
// 403 itself when not.
func (a *API) allowPCRForTeam(w http.ResponseWriter, r *http.Request, teamID uint, easyPCRDisabledMsg string) bool {
	if slices.Contains(r.Context().Value("roles").([]string), "admin") {
		return true
	}
	if !a.conf.MiscSettings.EasyPCR {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": easyPCRDisabledMsg})
		return false
	}
	myTeamID, hasTeam := CallerTeamID(r.Context())
	if !hasTeam {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Your account is not associated with a team"})
		return false
	}
	if teamID != myTeamID {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "PCR not allowed"})
		return false
	}
	return true
}

// pcrTeamID parses formTeamID, authorizes the caller for it and resolves it to
// a real team, writing the refusal itself when any of the three fails.
func (a *API) pcrTeamID(w http.ResponseWriter, r *http.Request, formTeamID string, easyPCRDisabledMsg string) (uint, bool) {
	parsed, err := strconv.ParseUint(formTeamID, 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return 0, false
	}
	teamID := uint(parsed)

	if !a.allowPCRForTeam(w, r, teamID, easyPCRDisabledMsg) {
		return 0, false
	}

	// Existence is checked after authorization, so a caller cannot tell a team
	// that does not exist from one that is not theirs.
	teams, err := a.eng.DB.GetTeams()
	if err != nil {
		WriteInternalError(w, r, "Error retrieving teams", err)
		return 0, false
	}
	if !slices.ContainsFunc(teams, func(t db.TeamSchema) bool { return t.ID == teamID }) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team ID"})
		return 0, false
	}
	return teamID, true
}

func (a *API) CreatePcr(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(w, r) {
		return
	}

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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&form)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		slog.Error("Failed to decode PCR json", "request_id", RequestID(r.Context()), "error", err.Error())
		return
	}

	teamID, ok := a.pcrTeamID(w, r, form.TeamID, "PCR not allowed")
	if !ok {
		return
	}

	id, _ := IdentityFrom(r.Context())
	changedBy := id.Username
	if changedBy == "" {
		changedBy, _ = r.Context().Value("username").(string)
	}
	updatedCount, skippedUsernames, err := a.eng.UpdateCredentials(teamID, form.CredlistPath, form.Usernames, form.Passwords, changedBy)
	if err != nil {
		WriteInternalError(w, r, "Error updating PCR", err)
		return
	}

	data := map[string]any{
		"message": "PCR updated successfully",
		"count":   updatedCount,
	}
	if len(skippedUsernames) > 0 {
		data["skipped"] = skippedUsernames
	}
	WriteJSON(w, http.StatusOK, data)
}

func (a *API) ResetPcr(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(w, r) {
		return
	}

	// get teamid from request
	// somehow determine which credlist to change
	type Form struct {
		TeamID       string `json:"team_id"`
		CredlistPath string `json:"credlist_id"`
	}

	var form Form
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&form)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		slog.Error("Failed to decode PCR json", "request_id", RequestID(r.Context()), "error", err.Error())
		return
	}
	teamID, ok := a.pcrTeamID(w, r, form.TeamID, "PCR reset not allowed")
	if !ok {
		return
	}

	id, _ := IdentityFrom(r.Context())
	changedBy := id.Username
	if changedBy == "" {
		changedBy, _ = r.Context().Value("username").(string)
	}
	if err := a.eng.ResetCredentials(teamID, form.CredlistPath, changedBy); err != nil {
		WriteInternalError(w, r, "Error resetting PCR", err)
		return
	}
	data := map[string]any{
		"message": "PCR reset successfully",
	}
	WriteJSON(w, http.StatusOK, data)
}
