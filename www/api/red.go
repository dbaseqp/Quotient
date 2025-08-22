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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	// load vulns from config/vulns.json
	file, err := os.Open("config/vulns.json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not open vulns.json: %v", err)}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	boxes, err := db.GetBoxes()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not get boxes: %v", err)}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	attacks, err := db.GetAttacks()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": fmt.Sprintf("could not get attacks: %v", err)}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	data := map[string]any{
		"vulns":   vulns,
		"boxes":   boxes,
		"teams":   teams,
		"attacks": attacks,
	}
	d, _ := json.Marshal(data)
	w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Box created successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func EditBox(w http.ResponseWriter, r *http.Request) {
	var id uint
	if temp, err := strconv.Atoi(r.FormValue("box-id")); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert box id"}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Box updated successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	var vuln uint
	if v, err := strconv.Atoi(a); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert vuln id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else {
		vuln = uint(v)
	}

	var box uint
	if v, err := strconv.Atoi(b); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert box id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else {
		box = uint(v)
	}

	port, err := strconv.Atoi(c)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to convert port"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}
	if port < 0 || port > 65535 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Port out of range"}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Vector created successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func EditVector(w http.ResponseWriter, r *http.Request) {

}

func CreateAttack(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to parse multipart form"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	pictures := r.MultipartForm.File["pictures"]
	filenames := make([]db.AttackImageSchema, len(pictures))
	for i, fileHeader := range pictures {
		filenames[i] = db.AttackImageSchema{
			URI: fileHeader.Filename,
		}
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else {
		vector = uint(v)
	}

	var team uint
	if v, err := strconv.Atoi(b); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert team id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	} else {
		team = uint(v)
	}

	access, err := strconv.Atoi(c)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to convert access level"}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		slog.Error("", "request_id", r.Context().Value("request_id"), "error", err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Attack created successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func EditAttack(w http.ResponseWriter, r *http.Request) {
}
