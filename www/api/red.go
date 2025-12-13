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
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not get teams: %v", err)}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	// load vulns from config/vulns.json
	file, err := os.Open("config/vulns.json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not open vulns.json: %v", err)}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	defer file.Close()

	var vulns []db.VulnSchema
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&vulns)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not decode vulns.json: %v", err)}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	boxes, err := db.GetBoxes()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not get boxes: %v", err)}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	attacks, err := db.GetAttacks()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not get attacks: %v", err)}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	data := map[string]any{
		"vulns":   vulns,
		"boxes":   boxes,
		"teams":   teams,
		"attacks": attacks,
	}
	WriteJSON(w, http.StatusOK, data)
}

func CreateBox(w http.ResponseWriter, r *http.Request) {
	ip := r.FormValue("ip")
	hostname := r.FormValue("hostname")

	box := db.BoxSchema{
		IP:       ip,
		Hostname: hostname,
	}

	if _, err := db.CreateBox(box); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to create box"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Box created successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func EditBox(w http.ResponseWriter, r *http.Request) {
	var id uint
	if temp, err := strconv.ParseUint(r.FormValue("box-id"), 10, 64); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert box id"}
		WriteJSON(w, http.StatusInternalServerError, data)
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
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to update box"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Box updated successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func CreateVector(w http.ResponseWriter, r *http.Request) {
	a := r.FormValue("vuln-id")
	b := r.FormValue("box-id")
	c := r.FormValue("port")

	description := r.FormValue("description")
	protocol := r.FormValue("protocol")

	if protocol != "tcp" && protocol != "udp" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid protocol"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	var vuln uint
	if v, err := strconv.Atoi(a); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert vuln id"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Vuln ID must be non-negative"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	} else {
		vuln = uint(v)
	}

	var box uint
	if v, err := strconv.Atoi(b); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert box id"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Box ID must be non-negative"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	} else {
		box = uint(v)
	}

	port, err := strconv.Atoi(c)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert port"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	if port < 0 || port > 65535 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Port out of range"}
		WriteJSON(w, http.StatusBadRequest, data)
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
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to create vector"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Vector created successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func EditVector(w http.ResponseWriter, r *http.Request) {

}

func CreateAttack(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to parse multipart form"}
		WriteJSON(w, http.StatusInternalServerError, data)
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
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert vector id"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Vector ID must be non-negative"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	} else {
		vector = uint(v)
	}

	var team uint
	if v, err := strconv.Atoi(b); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert team id"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else if v < 0 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Team ID must be non-negative"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	} else {
		team = uint(v)
	}

	access, err := strconv.Atoi(c)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert access level"}
		WriteJSON(w, http.StatusInternalServerError, data)
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
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to create attack"}
		WriteJSON(w, http.StatusInternalServerError, data)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Attack created successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func EditAttack(w http.ResponseWriter, r *http.Request) {
}
