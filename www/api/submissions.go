package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"quotient/engine/db"
	"slices"
	"strconv"
	"time"
)

func CreateSubmission(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid inject id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	injectID := uint(temp)
	username := r.Context().Value("username").(string)
	team, err := db.GetTeamByUsername(username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error retrieving team ID"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	err = r.ParseMultipartForm(50 << 20) // 50 MB
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Error parsing the form"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	if len(r.MultipartForm.File) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "No file uploaded"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Error retrieving the file"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	defer file.Close()

	submission := db.SubmissionSchema{
		TeamID:             team.ID,
		InjectID:           injectID,
		SubmissionTime:     time.Now(),
		SubmissionFileName: fileHeader.Filename,
	}

	injects, err := db.GetInjects()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error retrieving the injects"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	var inject db.InjectSchema
	for _, i := range injects {
		if i.ID == injectID {
			inject = i
			break
		}
	}

	if submission.SubmissionTime.After(inject.CloseTime) {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Inject is closed"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	submission, err = db.CreateSubmission(submission)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating the submission"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	uploadDir := fmt.Sprintf("submissions/%d/%d/%d", injectID, team.ID, submission.Version)
	err = os.MkdirAll(uploadDir, os.ModePerm)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating directories"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	out, err := os.Create(path.Join(uploadDir, fileHeader.Filename))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating the file"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error writing the file"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Inject submitted successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func DownloadSubmissionFile(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid inject id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	injectID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("team"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid team id"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	teamID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("version"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid version"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	version := uint(temp)

	username := r.Context().Value("username").(string)
	team, err := db.GetTeamByUsername(username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error retrieving team ID"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && !slices.Contains(req_roles, "inject") && team.ID != teamID {
		w.WriteHeader(http.StatusForbidden)
		data := map[string]any{"error": "Forbidden"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	submissions, err := db.GetSubmissionsForInject(injectID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error retrieving the submission"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	var submission db.SubmissionSchema
	for _, s := range submissions {
		if s.InjectID == injectID && s.TeamID == teamID && s.Version == int(version) {
			submission = s
			break
		}
	}

	if submission.Version == 0 { // use version because schema has no id
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "Submission not found"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	filePath := fmt.Sprintf("submissions/%d/%d/%d/%s", injectID, teamID, version, submission.SubmissionFileName)
	file, err := os.Open(filePath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error opening the file"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", "attachment; filename="+submission.SubmissionFileName)
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error sending the file"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
}
