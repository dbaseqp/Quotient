package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"quotient/engine/db"
	"strconv"
)

func GetRed(w http.ResponseWriter, r *http.Request) {
	teams, err := db.GetTeams()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("could not get teams: %v", err)})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	file, err := os.Open("config/vulns.json")
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("could not open vulns.json: %v", err)})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	defer file.Close()

	var vulns []db.VulnSchema
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&vulns); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("could not decode vulns.json: %v", err)})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	boxes, err := db.GetBoxes()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("could not get boxes: %v", err)})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	attacks, err := db.GetAttacks()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("could not get attacks: %v", err)})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"vulns":   vulns,
		"boxes":   boxes,
		"teams":   teams,
		"attacks": attacks,
	})
}

func CreateBox(w http.ResponseWriter, r *http.Request) {
	ip := r.FormValue("ip")
	hostname := r.FormValue("hostname")

	box := db.BoxSchema{
		IP:       ip,
		Hostname: hostname,
	}

	if _, err := db.CreateBox(box); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to create box"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Box created successfully"})
}

func EditBox(w http.ResponseWriter, r *http.Request) {
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

	if _, err := db.UpdateBox(box); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update box"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"message": "Box updated successfully"})
}

func CreateVector(w http.ResponseWriter, r *http.Request) {
	a := r.FormValue("vuln-id")
	b := r.FormValue("box-id")
	c := r.FormValue("port")

	description := r.FormValue("description")
	protocol := r.FormValue("protocol")

	if protocol != "tcp" && protocol != "udp" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid protocol"})
		return
	}

	var vuln uint
	if v, err := strconv.Atoi(a); err != nil {
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
	if v, err := strconv.Atoi(b); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert box id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Box ID must be non-negative"})
		return
	} else {
		box = uint(v)
	}

	port, err := strconv.Atoi(c)
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

	if _, err := db.CreateVector(vector); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to create vector"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Vector created successfully"})
}

func EditVector(w http.ResponseWriter, r *http.Request) {

}

func CreateAttack(w http.ResponseWriter, r *http.Request) {
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

	a := r.FormValue("vector-id")
	b := r.FormValue("team-id")
	c := r.FormValue("access-level")
	narrative := r.FormValue("narrative")

	active := r.FormValue("active") == "true"
	pii := r.FormValue("accessedpii") == "true"
	password := r.FormValue("accessedpassword") == "true"
	sysconfig := r.FormValue("accessedsysconfig") == "true"
	database := r.FormValue("accesseddatabases") == "true"

	var vector uint
	if v, err := strconv.Atoi(a); err != nil {
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
	if v, err := strconv.Atoi(b); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to convert team id"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Team ID must be non-negative"})
		return
	} else {
		team = uint(v)
	}

	access, err := strconv.Atoi(c)
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

	if _, err := db.CreateAttack(attack); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to create attack"})
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Attack created successfully"})
}

func EditAttack(w http.ResponseWriter, r *http.Request) {
}
