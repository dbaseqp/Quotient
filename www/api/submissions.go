package api

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
)

func (a *API) CreateSubmission(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid inject id"})
		return
	}
	injectID := uint(temp)
	teamID, hasTeam := CallerTeamID(r.Context())
	if !hasTeam {
		// A submission needs a team to attribute it to. Without one the
		// insert fails on the team_id foreign key and surfaces as a 500.
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Your account is not associated with a team"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<20) // 50 MB
	err = r.ParseMultipartForm(50 << 20)
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
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Error retrieving the file"})
		return
	}
	// nolint:errcheck
	defer file.Close()

	submission := db.SubmissionSchema{
		TeamID:             teamID,
		InjectID:           injectID,
		SubmissionTime:     time.Now(),
		SubmissionFileName: fileHeader.Filename,
	}

	injects, err := a.eng.DB.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving the injects"})
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
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Inject is closed"})
		return
	}

	submission, err = a.eng.DB.CreateSubmission(submission)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error creating the submission"})
		return
	}

	subDir := fmt.Sprintf("%d/%d/%d", injectID, teamID, submission.Version)
	err = SafeMkdirAll("submissions", subDir, 0750)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error creating directories"})
		return
	}

	uploadDir := filepath.Join("submissions", subDir)
	out, err := SafeCreate(uploadDir, fileHeader.Filename)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error creating the file"})
		return
	}
	// nolint:errcheck
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error writing the file"})
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Inject submitted successfully"})
}

func (a *API) DownloadSubmissionFile(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid inject id"})
		return
	}
	injectID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("team"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid team id"})
		return
	}
	teamID := uint(temp)

	temp, err = strconv.ParseUint(r.PathValue("version"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid version"})
		return
	}
	version := uint(temp)

	// Validate version fits in int to prevent overflow
	if version > math.MaxInt {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Version out of range"})
		return
	}

	if !requireOwnTeam(w, r, teamID, "admin", "inject") {
		return
	}

	submissions, err := a.eng.DB.GetSubmissionsForInject(injectID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving the submission"})
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
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Submission not found"})
		return
	}

	baseDir := fmt.Sprintf("submissions/%d/%d/%d", injectID, teamID, version)
	file, err := SafeOpen(baseDir, submission.SubmissionFileName)
	if err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "File not found"})
		return
	}
	// nolint:errcheck
	defer file.Close()

	w.Header().Set("Content-Disposition", "attachment; filename="+submission.SubmissionFileName)
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, file); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error sending the file"})
		return
	}
}

func (a *API) DownloadAllSubmissions(w http.ResponseWriter, r *http.Request) {
	temp, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid inject id"})
		return
	}
	injectID := uint(temp)

	if _, err := a.eng.DB.GetInjectByID(injectID); err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Inject not found"})
		return
	}

	submissions, err := a.eng.DB.GetSubmissionsForInject(injectID)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Error retrieving submissions"})
		return
	}

	if len(submissions) == 0 {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "No submissions found"})
		return
	}

	filename := fmt.Sprintf("inject%d_submissions.zip", injectID)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/zip")

	zipWriter := zip.NewWriter(w)
	// nolint:errcheck
	defer zipWriter.Close()

	// SubmissionFileName is validated by os.Root at upload time; Team.Name is admin-controlled.
	for _, submission := range submissions {
		baseDir := fmt.Sprintf("submissions/%d/%d/%d", injectID, submission.TeamID, submission.Version)
		file, err := SafeOpen(baseDir, submission.SubmissionFileName)
		if err != nil {
			continue
		}

		zipPath := filepath.Join(
			fmt.Sprintf("%s_v%d", submission.Team.Name, submission.Version),
			submission.SubmissionFileName,
		)

		zipEntry, err := zipWriter.Create(zipPath)
		if err != nil {
			slog.Error("failed to create zip entry", "path", zipPath, "error", err)
			if err := file.Close(); err != nil {
				slog.Error("failed to close file", "path", zipPath, "error", err)
			}
			continue
		}

		if _, err := io.Copy(zipEntry, file); err != nil {
			slog.Error("failed to copy file to zip", "path", zipPath, "error", err)
		}
		if err := file.Close(); err != nil {
			slog.Error("failed to close file", "path", zipPath, "error", err)
		}
	}
}
