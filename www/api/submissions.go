package api

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"quotient/engine/db"
	"slices"
	"strconv"
	"time"
)

func CreateSubmission(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid inject id"})
		return
	}
	injectID := uint(temp)
	username := r.Context().Value("username").(string)
	team, err := db.GetTeamByUsername(username)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving team ID"})
		return
	}

	err = r.ParseMultipartForm(50 << 20) // 50 MB
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Error parsing the form"})
		return
	}

	if len(r.MultipartForm.File) == 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "No file uploaded"})
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Error retrieving the file"}
		WriteJSON(w, http.StatusBadRequest, data)
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
		WriteJSON(w, http.StatusBadRequest, data)
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
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	submission, err = db.CreateSubmission(submission)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating the submission"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	uploadDir := fmt.Sprintf("submissions/%d/%d/%d", injectID, team.ID, submission.Version)
	err = os.MkdirAll(uploadDir, 0750)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating directories"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	out, err := SafeCreate(uploadDir, fileHeader.Filename)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error creating the file"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error writing the file"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Inject submitted successfully"}
	WriteJSON(w, http.StatusCreated, data)
}

func DownloadSubmissionFile(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid inject id"})
		return
	}
	injectID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("team"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid team id"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}
	teamID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("version"), 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid version"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}
	version := uint(temp)

	// Validate version fits in int to prevent overflow
	if version > math.MaxInt {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Version out of range"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	username := r.Context().Value("username").(string)
	team, err := db.GetTeamByUsername(username)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving team ID"})
		return
	}

	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && !slices.Contains(req_roles, "inject") && team.ID != teamID {
		w.WriteHeader(http.StatusForbidden)
		data := map[string]any{"error": "Forbidden"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	submissions, err := db.GetSubmissionsForInject(injectID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error retrieving the submission"}
		WriteJSON(w, http.StatusBadRequest, data)
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
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}

	baseDir := fmt.Sprintf("submissions/%d/%d/%d", injectID, teamID, version)
	file, err := SafeOpen(baseDir, submission.SubmissionFileName)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error opening the file"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", "attachment; filename="+submission.SubmissionFileName)
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Error sending the file"}
		WriteJSON(w, http.StatusBadRequest, data)
		return
	}
}
