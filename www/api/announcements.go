package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"

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
			WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
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
	announcementID := r.PathValue("id")
	if announcementID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing announcement ID"})
		return
	}

	fileName := r.PathValue("file")
	if fileName == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing file name"})
		return
	}

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
			WriteJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
			return
		}
		if time.Now().Before(announcement.OpenTime) {
			WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Announcement not found"})
			return
		}
	}

	// open file safely using os.Root to prevent path traversal
	baseDir := path.Join("submissions/announcements", announcementID)
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

	if announcement, err = db.CreateAnnouncement(announcement); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Announcement with the same title already exists"})
			return
		}
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)
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

	WriteJSON(w, http.StatusCreated, map[string]any{"message": "Announcement created successfully"})
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
		WriteJSON(w, http.StatusNotFound, map[string]any{"error": "Announcement not found"})
		return
	}

	if err := db.DeleteAnnouncement(announcement); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)
	if err := os.RemoveAll(uploadDir); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to remove announcement files"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"message": "Announcement deleted successfully"})
}
