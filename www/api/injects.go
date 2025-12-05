package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"quotient/engine/db"
	"slices"
	"time"

	"gorm.io/gorm"
)

func GetInjects(w http.ResponseWriter, r *http.Request) {
	data, err := db.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// if not admin filter out injects that are not open yet
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		openInjects := make([]db.InjectSchema, 0)
		for _, a := range data {
			if time.Now().After(a.OpenTime) {
				openInjects = append(openInjects, a)
			}
		}
		data = openInjects
	}

	for i, inject := range data {
		data[i].Submissions, err = db.GetSubmissionsForInject(inject.ID)
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !slices.Contains(req_roles, "admin") {
			var mySubmissions []db.SubmissionSchema
			for _, submission := range data[i].Submissions {
				if submission.Team.Name == r.Context().Value("username") {
					mySubmissions = append(mySubmissions, submission)
				}
			}
			data[i].Submissions = mySubmissions
		}
	}

	WriteJSON(w, http.StatusOK, data)
}

func DownloadInjectFile(w http.ResponseWriter, r *http.Request) {
	// get the inject id from the request
	injectID := r.PathValue("id")
	if injectID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing inject ID"})
		return
	}

	// get the file name from the request
	fileName := r.PathValue("file")
	if fileName == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing file name"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	// check if the inject exists
	injects, err := db.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var inject db.InjectSchema
	for _, a := range injects {
		if fmt.Sprint(a.ID) == injectID {
			inject = a
			break
		}
	}

	if inject.ID == 0 {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "Inject not found"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	// if not admin, check if the inject is open
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && time.Now().Before(inject.OpenTime) {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "Inject not found"} // don't leak if inject is not open
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	// get the file path
	baseDir := path.Join("config/injects", injectID)
	filePath := path.Join(baseDir, fileName)
	if !PathIsInDir(baseDir, filePath) {
		w.WriteHeader(http.StatusForbidden)
		data := map[string]any{"error": "Invalid file path"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	// check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "File not found"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	file, err := SafeOpen(baseDir, fileName)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to open file"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}
	defer file.Close()

	// set the headers
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "application/octet-stream")

	// send the file
	if _, err := io.Copy(w, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to send file"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}
}

func CreateInject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to parse multipart form"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	openTimeStr := r.FormValue("open-time")
	dueTimeStr := r.FormValue("due-time")
	closeTimeStr := r.FormValue("close-time")

	if title == "" || description == "" || openTimeStr == "" || dueTimeStr == "" || closeTimeStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing required fields"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	openTime, err := time.Parse(time.RFC3339, openTimeStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid open time format"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	dueTime, err := time.Parse(time.RFC3339, dueTimeStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid due time format"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	closeTime, err := time.Parse(time.RFC3339, closeTimeStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid close time format"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	files := r.MultipartForm.File["files"]
	filenames := make([]string, len(files))
	for i, fileHeader := range files {
		filenames[i] = fileHeader.Filename
	}

	inject := db.InjectSchema{
		Title:           title,
		Description:     description,
		OpenTime:        openTime,
		DueTime:         dueTime,
		CloseTime:       closeTime,
		InjectFileNames: filenames,
	}

	// ensure times are in order
	if inject.OpenTime.After(inject.DueTime) {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Open time must be before due time"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	if inject.DueTime.After(inject.CloseTime) {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Due time must be before close time"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	// create the inject, if successful id will be set in inject
	if inject, err = db.CreateInject(inject); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			w.WriteHeader(http.StatusBadRequest)
			data := map[string]any{"error": "Inject with the same title already exists"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}

		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// if successful save the files to the filesystem under config/injects/{inject.ID}
	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)

	if err := os.MkdirAll(uploadDir, 0750); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to create directory"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to open file"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
		defer file.Close()

		dst, err := os.Create(fmt.Sprintf("%s/%s", uploadDir, fileHeader.Filename))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to create file on disk"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to save file on disk"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Inject created successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func UpdateInject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to parse multipart form"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	injectID := r.PathValue("id")
	if injectID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing inject ID"})
		return
	}

	injects, err := db.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var inject db.InjectSchema
	for _, a := range injects {
		if fmt.Sprint(a.ID) == injectID {
			inject = a
			break
		}
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	openTimeStr := r.FormValue("open-time")
	dueTimeStr := r.FormValue("due-time")
	closeTimeStr := r.FormValue("close-time")

	// Get a list of filenames to keep
	existingFiles := r.Form["keep-files"]

	if title != "" {
		inject.Title = title
	}
	if description != "" {
		inject.Description = description
	}
	if openTimeStr != "" {
		openTime, err := time.Parse(time.RFC3339, openTimeStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			data := map[string]any{"error": "Invalid open time format"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
		inject.OpenTime = openTime
	}
	if dueTimeStr != "" {
		dueTime, err := time.Parse(time.RFC3339, dueTimeStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			data := map[string]any{"error": "Invalid due time format"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
		inject.DueTime = dueTime
	}
	if closeTimeStr != "" {
		closeTime, err := time.Parse(time.RFC3339, closeTimeStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			data := map[string]any{"error": "Invalid close time format"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
		inject.CloseTime = closeTime
	}

	// ensure times are in order
	if inject.OpenTime.After(inject.DueTime) {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Open time must be before due time"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	if inject.DueTime.After(inject.CloseTime) {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Due time must be before close time"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)

	// Get existing files in the directory
	dirFiles, err := os.ReadDir(uploadDir)
	if err != nil {
		if !os.IsNotExist(err) {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to read existing files"}
			WriteJSON(w, http.StatusBadRequest, data)
			return
		}
	}

	// Create a map of existing files to keep for quick lookup
	existingFilesMap := make(map[string]struct{})
	for _, fileName := range existingFiles {
		existingFilesMap[fileName] = struct{}{}
	}

	// Delete existing files that are not in the existingFiles slice
	for _, dirFile := range dirFiles {
		if _, exists := existingFilesMap[dirFile.Name()]; !exists {
			if err := os.Remove(path.Join(uploadDir, dirFile.Name())); err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to remove old file"})
				return
			} else {
				// Remove the file from the database
				inject.InjectFileNames = slices.DeleteFunc(inject.InjectFileNames, func(filename string) bool {
					return filename == dirFile.Name()
				})
			}
		}
	}

	files := r.MultipartForm.File["files"]
	if len(files) > 0 {
		var filenames []string
		for _, fileHeader := range files {
			filenames = append(filenames, fileHeader.Filename)
		}
		inject.InjectFileNames = append(inject.InjectFileNames, filenames...)

		for _, fileHeader := range files {
			file, err := fileHeader.Open()
			if err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to open file"})
				return
			}
			defer file.Close()

			dst, err := os.Create(fmt.Sprintf("%s/%s", uploadDir, fileHeader.Filename))
			if err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to create file on disk"})
				return
			}
			defer dst.Close()

			if _, err := io.Copy(dst, file); err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to save file on disk"})
				return
			}
		}
	}

	if _, err := db.UpdateInject(inject); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	data := map[string]any{"message": "Inject updated successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func DeleteInject(w http.ResponseWriter, r *http.Request) {
	injectID := r.PathValue("id")
	if injectID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing inject ID"})
		return
	}

	injects, err := db.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var inject db.InjectSchema
	for _, a := range injects {
		if fmt.Sprint(a.ID) == injectID {
			inject = a
			break
		}
	}

	if inject.ID == 0 {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "Inject not found"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	if err := db.DeleteInject(inject); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)
	if err := os.RemoveAll(uploadDir); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to remove inject files"}
		d, err := json.Marshal(data)
		if err != nil {
			slog.Error("failed to marshal error response", "error", err)
			return
		}
		if _, err := w.Write(d); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	data := map[string]any{"message": "Inject deleted successfully"}
	WriteJSON(w, http.StatusOK, data)
}
