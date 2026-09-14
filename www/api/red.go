package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/dbaseqp/Quotient/engine/db"
)

func (a *API) GetRed(w http.ResponseWriter, r *http.Request) {
	teams, err := a.eng.DB.GetTeams()
	if err != nil {
		WriteInternalError(w, r, "Error retrieving teams", err)
		return
	}

	file, err := os.Open("config/vulns.json")
	if err != nil {
		WriteInternalError(w, r, "Error opening vulnerability data", err)
		return
	}
	// nolint:errcheck
	defer file.Close()

	var vulns []db.VulnSchema
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&vulns); err != nil {
		WriteInternalError(w, r, "Error decoding vulnerability data", err)
		return
	}

	boxes, err := a.eng.DB.GetBoxes()
	if err != nil {
		WriteInternalError(w, r, "Error retrieving boxes", err)
		return
	}

	attacks, err := a.eng.DB.GetAttacks()
	if err != nil {
		WriteInternalError(w, r, "Error retrieving attacks", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"vulns":   vulns,
		"boxes":   boxes,
		"teams":   teams,
		"attacks": attacks,
	})
}

func (a *API) CreateBox(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	ip := r.FormValue("ip")
	hostname := r.FormValue("hostname")

	box := db.BoxSchema{
		IP:       ip,
		Hostname: hostname,
	}

	if _, err := a.eng.DB.CreateBox(box); err != nil {
		WriteInternalError(w, r, "Failed to create box", err)
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Box created successfully"})
}

func (a *API) EditBox(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var id uint
	if temp, err := strconv.ParseUint(r.FormValue("box-id"), 10, 64); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert box id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else {
		id = uint(temp)
	}
	ip := r.FormValue("ip")
	hostname := r.FormValue("hostname")

	box := db.BoxSchema{
		ID:       id,
		IP:       ip,
		Hostname: hostname,
	}

	if _, err := a.eng.DB.UpdateBox(box); err != nil {
		WriteInternalError(w, r, "Failed to update box", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"message": "Box updated successfully"})
}

func (a *API) CreateVector(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	vulnID := r.FormValue("vuln-id")
	boxID := r.FormValue("box-id")
	portStr := r.FormValue("port")

	description := r.FormValue("description")
	protocol := r.FormValue("protocol")

	if protocol != "tcp" && protocol != "udp" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid protocol"})
		return
	}

	var vuln uint
	if v, err := strconv.Atoi(vulnID); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert vuln id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Vuln ID must be non-negative"})
		return
	} else {
		vuln = uint(v)
	}

	var box uint
	if v, err := strconv.Atoi(boxID); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert box id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Box ID must be non-negative"})
		return
	} else {
		box = uint(v)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert port"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	if port < 0 || port > 65535 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Port out of range"})
		return
	}

	vector := db.VectorSchema{
		VulnID:                    vuln,
		BoxID:                     box,
		Port:                      port,
		Protocol:                  protocol,
		ImplementationDescription: description,
	}

	if _, err := a.eng.DB.CreateVector(vector); err != nil {
		WriteInternalError(w, r, "Failed to create vector", err)
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Vector created successfully"})
}

func (a *API) EditVector(w http.ResponseWriter, r *http.Request) {

}

func (a *API) CreateAttack(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to parse multipart form"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	pictures := r.MultipartForm.File["pictures"]
	filenames := make([]string, len(pictures))
	for i, fileHeader := range pictures {
		filenames[i] = fileHeader.Filename
	}

	vectorID := r.FormValue("vector-id")
	teamID := r.FormValue("team-id")
	accessLevelStr := r.FormValue("access-level")
	narrative := r.FormValue("narrative")

	active := r.FormValue("active") == "true"
	pii := r.FormValue("accessedpii") == "true"
	password := r.FormValue("accessedpassword") == "true"
	sysconfig := r.FormValue("accessedsysconfig") == "true"
	database := r.FormValue("accesseddatabases") == "true"

	var vector uint
	if v, err := strconv.Atoi(vectorID); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert vector id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Vector ID must be non-negative"})
		return
	} else {
		vector = uint(v)
	}

	var team uint
	if v, err := strconv.Atoi(teamID); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert team id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Team ID must be non-negative"})
		return
	} else {
		team = uint(v)
	}

	access, err := strconv.Atoi(accessLevelStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert access level"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	attack := db.AttackSchema{
		VectorID:                      vector,
		TeamID:                        team,
		Narrative:                     narrative,
		EvidenceImages:                filenames,
		StillWorks:                    active,
		AccessLevel:                   access,
		DataAccessPII:                 pii,
		DataAccessPassword:            password,
		DataAccessSystemConfiguration: sysconfig,
		DataAccessDatabase:            database,
	}

	if _, err := a.eng.DB.CreateAttack(attack); err != nil {
		WriteInternalError(w, r, "Failed to create attack", err)
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Attack created successfully"})
}

func (a *API) EditAttack(w http.ResponseWriter, r *http.Request) {
}
