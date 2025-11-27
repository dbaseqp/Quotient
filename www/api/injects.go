package api

import (
	"errors"
	"fmt"
	"io"
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
	injectID := r.PathValue("id")
	if injectID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing inject ID"})
		return
	}

	fileName := r.PathValue("file")
	if fileName == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing file name"})
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
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Inject not found"})
		return
	}

	// if not admin, check if the inject is open
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") && time.Now().Before(inject.OpenTime) {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Inject not found"})
		return
	}

	// open file safely using os.Root to prevent path traversal
	baseDir := path.Join("config/injects", injectID)
	file, err := SafeOpen(baseDir, fileName)
	if err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "File not found"})
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "application/octet-stream")

	if _, err := io.Copy(w, file); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to send file"})
		return
	}
}

func CreateInject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to parse multipart form"})
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	openTimeStr := r.FormValue("open-time")
	dueTimeStr := r.FormValue("due-time")
	closeTimeStr := r.FormValue("close-time")

	if title == "" || description == "" || openTimeStr == "" || dueTimeStr == "" || closeTimeStr == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing required fields"})
		return
	}

	openTime, err := time.Parse(time.RFC3339, openTimeStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid open time format"})
		return
	}

	dueTime, err := time.Parse(time.RFC3339, dueTimeStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid due time format"})
		return
	}

	closeTime, err := time.Parse(time.RFC3339, closeTimeStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid close time format"})
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

	if inject.OpenTime.After(inject.DueTime) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Open time must be before due time"})
		return
	}

	if inject.DueTime.After(inject.CloseTime) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Due time must be before close time"})
		return
	}

	if inject, err = db.CreateInject(inject); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Inject with the same title already exists"})
			return
		}
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)
	if err := os.MkdirAll(uploadDir, 0750); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to create directory"})
		return
	}

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

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Inject created successfully"})
}

func UpdateInject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to parse multipart form"})
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
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid open time format"})
			return
		}
		inject.OpenTime = openTime
	}
	if dueTimeStr != "" {
		dueTime, err := time.Parse(time.RFC3339, dueTimeStr)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid due time format"})
			return
		}
		inject.DueTime = dueTime
	}
	if closeTimeStr != "" {
		closeTime, err := time.Parse(time.RFC3339, closeTimeStr)
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid close time format"})
			return
		}
		inject.CloseTime = closeTime
	}

	if inject.OpenTime.After(inject.DueTime) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Open time must be before due time"})
		return
	}

	if inject.DueTime.After(inject.CloseTime) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Due time must be before close time"})
		return
	}

	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)

	dirFiles, err := os.ReadDir(uploadDir)
	if err != nil && !os.IsNotExist(err) {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to read existing files"})
		return
	}

	existingFilesMap := make(map[string]struct{})
	for _, fileName := range existingFiles {
		existingFilesMap[fileName] = struct{}{}
	}

	for _, dirFile := range dirFiles {
		if _, exists := existingFilesMap[dirFile.Name()]; !exists {
			if err := os.Remove(path.Join(uploadDir, dirFile.Name())); err != nil {
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to remove old file"})
				return
			}
			inject.InjectFileNames = slices.DeleteFunc(inject.InjectFileNames, func(filename string) bool {
				return filename == dirFile.Name()
			})
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

	WriteJSON(w, http.StatusOK, map[string]any{"message": "Inject updated successfully"})
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
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Inject not found"})
		return
	}

	if err := db.DeleteInject(inject); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	uploadDir := fmt.Sprintf("config/injects/%d", inject.ID)
	if err := os.RemoveAll(uploadDir); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to remove inject files"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"message": "Inject deleted successfully"})
}
