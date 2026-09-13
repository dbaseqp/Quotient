package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/dbaseqp/Quotient/engine/db"
	"gorm.io/gorm"
)

const (
	maxInjectBundleSize    = 100 << 20
	maxInjectExpandedSize  = 250 << 20
	maxInjectArchiveFiles  = 500
	injectManifestFileName = "injects.toml"
)

type injectImportDefinition struct {
	Title       string
	Description string
	OpenAfter   string
	DueAfter    string
	CloseAfter  string
	Files       []string
}

type injectImportManifest struct {
	Inject []injectImportDefinition
}

type preparedInject struct {
	Schema db.InjectSchema
	Files  map[string][]byte
}

type injectImportPreview struct {
	Title     string    `json:"title"`
	OpenTime  time.Time `json:"open_time"`
	DueTime   time.Time `json:"due_time"`
	CloseTime time.Time `json:"close_time"`
	Files     []string  `json:"files"`
}

func ImportInjects(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxInjectBundleSize)
	if err := r.ParseMultipartForm(maxInjectBundleSize); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "The import bundle is invalid or exceeds 100 MB"})
		return
	}

	bundleHeaders := r.MultipartForm.File["bundle"]
	if len(bundleHeaders) != 1 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "An import ZIP bundle is required"})
		return
	}

	fileHeader := bundleHeaders[0]
	bundle, err := fileHeader.Open()
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Failed to open import bundle"})
		return
	}
	// nolint:errcheck
	defer bundle.Close()

	archive, err := zip.NewReader(bundle, fileHeader.Size)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Import bundle must be a valid ZIP file"})
		return
	}

	anchor, anchorSource, err := injectScheduleAnchor()
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	existing, err := db.GetInjects()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to check existing injects"})
		return
	}
	existingTitles := make(map[string]struct{}, len(existing))
	for _, inject := range existing {
		existingTitles[inject.Title] = struct{}{}
	}

	prepared, err := prepareInjectImport(archive, anchor, existingTitles)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	preview := make([]injectImportPreview, len(prepared))
	for i := range prepared {
		preview[i] = injectImportPreview{
			Title:     prepared[i].Schema.Title,
			OpenTime:  prepared[i].Schema.OpenTime,
			DueTime:   prepared[i].Schema.DueTime,
			CloseTime: prepared[i].Schema.CloseTime,
			Files:     prepared[i].Schema.InjectFileNames,
		}
	}

	if r.URL.Query().Get("preview") == "1" {
		WriteJSON(w, http.StatusOK, map[string]any{
			"anchor":        anchor,
			"anchor_source": anchorSource,
			"injects":       preview,
		})
		return
	}

	created, err := createImportedInjects(prepared)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			WriteJSON(w, http.StatusConflict, map[string]any{"error": "An inject title already exists"})
			return
		}
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to import injects"})
		return
	}

	// If the first competition start raced with this import, ensure the newly
	// created rows use the persisted actual start rather than the planned one.
	latestStart, err := db.GetCompetitionStart()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Injects were imported, but the competition start could not be verified"})
		return
	}
	if latestStart != nil && !latestStart.Equal(anchor) {
		if err := db.RecalculateImportedInjectTimes(*latestStart); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Injects were imported, but their schedule could not be updated to the actual start"})
			return
		}
		anchor = *latestStart
		anchorSource = "actual"
		for i := range preview {
			preview[i].OpenTime = anchor.Add(time.Duration(*prepared[i].Schema.OpenOffset) * time.Second)
			preview[i].DueTime = anchor.Add(time.Duration(*prepared[i].Schema.DueOffset) * time.Second)
			preview[i].CloseTime = anchor.Add(time.Duration(*prepared[i].Schema.CloseOffset) * time.Second)
		}
	}

	WriteJSON(w, http.StatusCreated, map[string]any{
		"message":       fmt.Sprintf("Imported %d injects", len(created)),
		"anchor":        anchor,
		"anchor_source": anchorSource,
		"injects":       preview,
	})
}

func injectScheduleAnchor() (time.Time, string, error) {
	actualStart, err := db.GetCompetitionStart()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to read competition start: %w", err)
	}
	if actualStart != nil {
		return *actualStart, "actual", nil
	}

	plannedStart, err := conf.CompetitionStart()
	if err != nil {
		return time.Time{}, "", err
	}
	return plannedStart, "planned", nil
}

func prepareInjectImport(archive *zip.Reader, anchor time.Time, existingTitles map[string]struct{}) ([]preparedInject, error) {
	if len(archive.File) > maxInjectArchiveFiles {
		return nil, fmt.Errorf("import bundle contains more than %d files", maxInjectArchiveFiles)
	}

	archiveFiles := make(map[string][]byte)
	var expandedSize uint64
	for _, archivedFile := range archive.File {
		if archivedFile.FileInfo().IsDir() || strings.HasPrefix(archivedFile.Name, "__MACOSX/") {
			continue
		}
		cleanName := path.Clean(archivedFile.Name)
		if cleanName != archivedFile.Name || path.IsAbs(cleanName) || strings.Contains(cleanName, "/") || cleanName == "." {
			return nil, fmt.Errorf("archive files must be in the ZIP root: %q", archivedFile.Name)
		}
		expandedSize += archivedFile.UncompressedSize64
		if expandedSize > maxInjectExpandedSize {
			return nil, errors.New("expanded import bundle exceeds 250 MB")
		}
		remaining := uint64(maxInjectExpandedSize) - (expandedSize - archivedFile.UncompressedSize64)
		contents, err := readZipFile(archivedFile, remaining)
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", cleanName, err)
		}
		if _, exists := archiveFiles[cleanName]; exists {
			return nil, fmt.Errorf("duplicate archive file %q", cleanName)
		}
		archiveFiles[cleanName] = contents
	}

	manifestBytes, ok := archiveFiles[injectManifestFileName]
	if !ok {
		return nil, fmt.Errorf("%s is required in the ZIP root", injectManifestFileName)
	}
	var manifest injectImportManifest
	metadata, err := toml.Decode(string(manifestBytes), &manifest)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", injectManifestFileName, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unknown field in %s: %s", injectManifestFileName, undecoded[0].String())
	}
	if len(manifest.Inject) == 0 {
		return nil, errors.New("injects.toml must contain at least one [[Inject]] entry")
	}

	titles := make(map[string]struct{}, len(existingTitles)+len(manifest.Inject))
	for title := range existingTitles {
		titles[title] = struct{}{}
	}

	prepared := make([]preparedInject, 0, len(manifest.Inject))
	for i, definition := range manifest.Inject {
		entry, err := prepareInjectDefinition(definition, anchor, archiveFiles, titles)
		if err != nil {
			return nil, fmt.Errorf("inject %d: %w", i+1, err)
		}
		titles[entry.Schema.Title] = struct{}{}
		prepared = append(prepared, entry)
	}
	return prepared, nil
}

func prepareInjectDefinition(definition injectImportDefinition, anchor time.Time, archiveFiles map[string][]byte, titles map[string]struct{}) (preparedInject, error) {
	definition.Title = strings.TrimSpace(definition.Title)
	if definition.Title == "" {
		return preparedInject{}, errors.New("title is required")
	}
	if _, exists := titles[definition.Title]; exists {
		return preparedInject{}, fmt.Errorf("inject title %q already exists or is duplicated", definition.Title)
	}
	if definition.Description == "" {
		definition.Description = "See attached files."
	}

	openAfter, err := parseInjectOffset("OpenAfter", definition.OpenAfter, true)
	if err != nil {
		return preparedInject{}, err
	}
	dueAfter, err := parseInjectOffset("DueAfter", definition.DueAfter, true)
	if err != nil {
		return preparedInject{}, err
	}
	closeValue := definition.CloseAfter
	if closeValue == "" {
		closeValue = definition.DueAfter
	}
	closeAfter, err := parseInjectOffset("CloseAfter", closeValue, true)
	if err != nil {
		return preparedInject{}, err
	}
	if openAfter > dueAfter {
		return preparedInject{}, errors.New("OpenAfter must be before or equal to DueAfter")
	}
	if dueAfter > closeAfter {
		return preparedInject{}, errors.New("DueAfter must be before or equal to CloseAfter")
	}

	files := make(map[string][]byte, len(definition.Files))
	seenFiles := make(map[string]struct{}, len(definition.Files))
	for _, fileName := range definition.Files {
		if fileName == "" || filepath.Base(fileName) != fileName || strings.Contains(fileName, "\\") || fileName == injectManifestFileName {
			return preparedInject{}, fmt.Errorf("invalid attachment name %q", fileName)
		}
		if _, exists := seenFiles[fileName]; exists {
			return preparedInject{}, fmt.Errorf("attachment %q is listed more than once", fileName)
		}
		contents, exists := archiveFiles[fileName]
		if !exists {
			return preparedInject{}, fmt.Errorf("attachment %q is missing from the ZIP", fileName)
		}
		seenFiles[fileName] = struct{}{}
		files[fileName] = contents
	}

	openSeconds := int64(openAfter / time.Second)
	dueSeconds := int64(dueAfter / time.Second)
	closeSeconds := int64(closeAfter / time.Second)
	return preparedInject{
		Schema: db.InjectSchema{
			Title:           definition.Title,
			Description:     definition.Description,
			OpenTime:        anchor.Add(openAfter),
			DueTime:         anchor.Add(dueAfter),
			CloseTime:       anchor.Add(closeAfter),
			InjectFileNames: slices.Clone(definition.Files),
			OpenOffset:      &openSeconds,
			DueOffset:       &dueSeconds,
			CloseOffset:     &closeSeconds,
		},
		Files: files,
	}, nil
}

func parseInjectOffset(field, value string, required bool) (time.Duration, error) {
	if value == "" {
		if required {
			return 0, fmt.Errorf("%s is required", field)
		}
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 30m or 2h15m", field)
	}
	if duration < 0 || duration%time.Second != 0 {
		return 0, fmt.Errorf("%s must be a non-negative whole-second duration", field)
	}
	return duration, nil
}

func readZipFile(file *zip.File, limit uint64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer reader.Close()
	contents, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(contents)) > limit {
		return nil, errors.New("expanded import bundle exceeds 250 MB")
	}
	return contents, nil
}

func createImportedInjects(prepared []preparedInject) ([]db.InjectSchema, error) {
	schemas := make([]db.InjectSchema, len(prepared))
	for i := range prepared {
		schemas[i] = prepared[i].Schema
	}
	created, err := db.CreateInjectBatch(schemas)
	if err != nil {
		return nil, err
	}

	createdIDs := make([]uint, len(created))
	createdDirs := make([]string, 0, len(created))
	cleanup := func() {
		for _, dir := range createdDirs {
			_ = os.RemoveAll(dir)
		}
		_ = db.DeleteInjectBatch(createdIDs)
	}

	for i, inject := range created {
		createdIDs[i] = inject.ID
		subDir := fmt.Sprintf("%d", inject.ID)
		if err := SafeMkdirAll("config/injects", subDir, 0750); err != nil {
			cleanup()
			return nil, err
		}
		uploadDir := filepath.Join("config/injects", subDir)
		createdDirs = append(createdDirs, uploadDir)
		for fileName, contents := range prepared[i].Files {
			destination, err := SafeCreate(uploadDir, fileName)
			if err != nil {
				cleanup()
				return nil, err
			}
			_, copyErr := io.Copy(destination, bytes.NewReader(contents))
			closeErr := destination.Close()
			if copyErr != nil || closeErr != nil {
				cleanup()
				return nil, errors.Join(copyErr, closeErr)
			}
		}
	}
	return created, nil
}
