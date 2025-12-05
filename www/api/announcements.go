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

func GetAnnouncements(w http.ResponseWriter, r *http.Request) {
	data, err := db.GetAnnouncements()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// if not admin filter out announcements that are not open yet
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		if slices.Contains(req_roles, "red") && !conf.UISettings.ShowAnnouncementsForRedTeam {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		openAnnouncements := make([]db.AnnouncementSchema, 0)
		for _, a := range data {
			if time.Now().After(a.OpenTime) {
				openAnnouncements = append(openAnnouncements, a)
			}
		}
		data = openAnnouncements
	}

	WriteJSON(w, http.StatusOK, data)
}

func DownloadAnnouncementFile(w http.ResponseWriter, r *http.Request) {
	// get the announcement id from the request
	announcementID := r.PathValue("id")
	if announcementID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing announcement ID"})
		return
	}

	// get the file name from the request
	fileName := r.PathValue("file")
	if fileName == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing file name"})
		return
	}

	// check if the announcement exists
	announcements, err := db.GetAnnouncements()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Announcement not found"})
			return
		}

		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var announcement db.AnnouncementSchema
	for _, a := range announcements {
		if fmt.Sprint(a.ID) == announcementID {
			announcement = a
			break
		}
	}

	// if not admin check if the announcement is open
	req_roles := r.Context().Value("roles").([]string)
	if !slices.Contains(req_roles, "admin") {
		if slices.Contains(req_roles, "red") && !conf.UISettings.ShowAnnouncementsForRedTeam {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if time.Now().Before(announcement.OpenTime) {
			WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Announcement not found"})
			return
		}
	}

	// get the file path
	baseDir := path.Join("submissions/announcements", announcementID)
	filePath := path.Join(baseDir, fileName)
	if !PathIsInDir(baseDir, filePath) {
		WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Invalid file path"})
		return
	}

	// check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "File not found"})
		return
	}

	file, err := SafeOpen(baseDir, fileName)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to open file"})
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

func CreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to parse multipart form"})
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	openTimeStr := r.FormValue("open-time")

	if title == "" || description == "" || openTimeStr == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing required fields"})
		return
	}

	openTime, err := time.Parse(time.RFC3339, openTimeStr)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid open time format"})
		return
	}

	files := r.MultipartForm.File["files"]
	filenames := make([]string, len(files))
	for i, fileHeader := range files {
		filenames[i] = fileHeader.Filename
	}

	announcement := db.AnnouncementSchema{
		Title:                 title,
		Description:           description,
		OpenTime:              openTime,
		AnnouncementFileNames: filenames,
	}

	// create the announcement, if successful id will be set in announcement
	if announcement, err = db.CreateAnnouncement(announcement); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			w.WriteHeader(http.StatusBadRequest)
			data := map[string]any{"error": "Announcement with the same title already exists"}
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

		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// if successful save the files to the filesystem under submissions/announcements/{announcement.ID}
	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)

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

		dst, err := os.Create(fmt.Sprintf("%s/%s", uploadDir, fileHeader.Filename))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to create file on disk"}
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
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to save file on disk"}
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

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Announcement created successfully"}
	WriteJSON(w, http.StatusOK, data)
}

func UpdateAnnouncement(w http.ResponseWriter, r *http.Request) {

}

func DeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	announcementID := r.PathValue("id")
	if announcementID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing announcement ID"})
		return
	}

	announcements, err := db.GetAnnouncements()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var announcement db.AnnouncementSchema
	for _, a := range announcements {
		if fmt.Sprint(a.ID) == announcementID {
			announcement = a
			break
		}
	}

	if announcement.ID == 0 {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "Announcement not found"}
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

	if err := db.DeleteAnnouncement(announcement); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// delete the files from the filesystem
	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)
	if err := os.RemoveAll(uploadDir); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to remove announcement files"}
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
	data := map[string]any{"message": "Announcement deleted successfully"}
	WriteJSON(w, http.StatusOK, data)
}
