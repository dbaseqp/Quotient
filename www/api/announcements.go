package api

import (
	"encoding/json"
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

func GetAnnouncements(w http.ResponseWriter, r *http.Request) {
	data, err := db.GetAnnouncements()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
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

	d, _ := json.Marshal(data)
	w.Write(d)
}

func DownloadAnnouncementFile(w http.ResponseWriter, r *http.Request) {
	// get the announcement id from the request
	announcementID := r.PathValue("id")
	if announcementID == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing announcement ID"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	// get the file name from the request
	fileName := r.PathValue("file")
	if fileName == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing file name"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	// check if the announcement exists
	announcements, err := db.GetAnnouncements()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			w.WriteHeader(http.StatusNotFound)
			data := map[string]any{"error": "Announcement not found"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
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
			w.WriteHeader(http.StatusNotFound)
			data := map[string]any{"error": "Announcement not found"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}
	}

	// get the file path
	filePath := path.Join("submissions/announcements", announcementID, fileName)
	if !PathIsInDir("submissions/announcements", filePath) {
		w.WriteHeader(http.StatusForbidden)
		data := map[string]any{"error": "Invalid file path"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	// check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		w.WriteHeader(http.StatusNotFound)
		data := map[string]any{"error": "File not found"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to open file"}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}
}

func CreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Failed to parse multipart form"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	openTimeStr := r.FormValue("open-time")

	if title == "" || description == "" || openTimeStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing required fields"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	openTime, err := time.Parse(time.RFC3339, openTimeStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Invalid open time format"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	files := r.MultipartForm.File["files"]
	filenames := make([]db.AnnouncementFileSchema, len(files))
	for i, fileHeader := range files {
		filenames[i] = db.AnnouncementFileSchema{
			FileName: fileHeader.Filename,
		}
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
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	// if successful save the files to the filesystem under submissions/announcements/{announcement.ID}
	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)

	if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to create directory"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to open file"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}
		defer file.Close()

		dst, err := os.Create(fmt.Sprintf("%s/%s", uploadDir, fileHeader.Filename))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to create file on disk"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			data := map[string]any{"error": "Failed to save file on disk"}
			d, _ := json.Marshal(data)
			w.Write(d)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
	data := map[string]any{"message": "Announcement created successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}

func UpdateAnnouncement(w http.ResponseWriter, r *http.Request) {

}

func DeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	announcementID := r.PathValue("id")
	if announcementID == "" {
		w.WriteHeader(http.StatusBadRequest)
		data := map[string]any{"error": "Missing announcement ID"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	announcements, err := db.GetAnnouncements()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
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
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	if err := db.DeleteAnnouncement(announcement); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": err.Error()}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	// delete the files from the filesystem
	uploadDir := fmt.Sprintf("submissions/announcements/%d", announcement.ID)
	if err := os.RemoveAll(uploadDir); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data := map[string]any{"error": "Failed to remove announcement files"}
		d, _ := json.Marshal(data)
		w.Write(d)
		return
	}

	w.WriteHeader(http.StatusOK)
	data := map[string]any{"message": "Announcement deleted successfully"}
	d, _ := json.Marshal(data)
	w.Write(d)
}
