package integration

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"quotient/engine/db"
	"quotient/tests/testutil"
	"quotient/www/api"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadAllSubmissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pgContainer := testutil.StartPostgres(t)
	defer pgContainer.Close()
	db.Connect(pgContainer.ConnectionString())

	// Use temp dir for submission files
	submissionsDir := t.TempDir()
	originalWd, _ := os.Getwd()
	os.Chdir(submissionsDir)
	defer os.Chdir(originalWd)

	// Setup: team, inject, submissions
	team, err := db.CreateTeam(db.TeamSchema{
		Name:       fmt.Sprintf("Team-%d", time.Now().UnixNano()),
		Identifier: "01",
		Active:     true,
	})
	require.NoError(t, err, "failed to create team")
	t.Logf("Created team ID: %d", team.ID)

	inject, err := db.CreateInject(db.InjectSchema{
		Title:     fmt.Sprintf("Test Inject-%d", time.Now().UnixNano()),
		OpenTime:  time.Now().Add(-1 * time.Hour),
		DueTime:   time.Now().Add(1 * time.Hour),
		CloseTime: time.Now().Add(2 * time.Hour),
	})
	require.NoError(t, err, "failed to create inject")
	t.Logf("Created inject ID: %d", inject.ID)

	// Create 2 submission versions
	for v := 1; v <= 2; v++ {
		dir := filepath.Join("submissions", fmt.Sprintf("%d/%d/%d", inject.ID, team.ID, v))
		err := os.MkdirAll(dir, 0750)
		require.NoError(t, err, "failed to create dir")

		filename := fmt.Sprintf("report_v%d.pdf", v)
		err = os.WriteFile(filepath.Join(dir, filename), []byte(fmt.Sprintf("content %d", v)), 0644)
		require.NoError(t, err, "failed to write file")

		sub, err := db.CreateSubmission(db.SubmissionSchema{
			TeamID:             team.ID,
			InjectID:           inject.ID,
			SubmissionTime:     time.Now(),
			SubmissionFileName: filename,
		})
		require.NoError(t, err, "failed to create submission")
		t.Logf("Created submission version %d for team %d", sub.Version, team.ID)
	}

	// Verify submissions exist
	subs, err := db.GetSubmissionsForInject(inject.ID)
	require.NoError(t, err)
	t.Logf("Found %d submissions for inject %d", len(subs), inject.ID)

	// Make request
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/injects/%d/submissions/download", inject.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", inject.ID))
	rr := httptest.NewRecorder()

	api.DownloadAllSubmissions(rr, req)

	// Verify ZIP response
	if rr.Code != http.StatusOK {
		t.Logf("Response body: %s", rr.Body.String())
	}
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/zip", rr.Header().Get("Content-Type"))

	zipReader, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)
	require.Len(t, zipReader.File, 2)

	// Verify files are in ZIP with correct content
	for _, f := range zipReader.File {
		rc, _ := f.Open()
		content, _ := io.ReadAll(rc)
		rc.Close()
		assert.Contains(t, string(content), "content")
	}
}

func TestDownloadAllSubmissions_MultipleTeamsSameFilename(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pgContainer := testutil.StartPostgres(t)
	defer pgContainer.Close()
	db.Connect(pgContainer.ConnectionString())

	// Use temp dir for submission files
	submissionsDir := t.TempDir()
	originalWd, _ := os.Getwd()
	os.Chdir(submissionsDir)
	defer os.Chdir(originalWd)

	// Create inject
	inject, err := db.CreateInject(db.InjectSchema{
		Title:     fmt.Sprintf("Multi-Team Inject-%d", time.Now().UnixNano()),
		OpenTime:  time.Now().Add(-1 * time.Hour),
		DueTime:   time.Now().Add(1 * time.Hour),
		CloseTime: time.Now().Add(2 * time.Hour),
	})
	require.NoError(t, err)
	t.Logf("Created inject ID: %d", inject.ID)

	// Create 3 teams, each submitting a file with the same name
	numTeams := 3
	teams := make([]db.TeamSchema, numTeams)
	for i := 0; i < numTeams; i++ {
		team, err := db.CreateTeam(db.TeamSchema{
			Name:       fmt.Sprintf("Team%d-%d", i+1, time.Now().UnixNano()),
			Identifier: fmt.Sprintf("%02d", i+1),
			Active:     true,
		})
		require.NoError(t, err)
		teams[i] = team
		t.Logf("Created team: %s (ID: %d)", team.Name, team.ID)

		// Each team submits "report.pdf" with unique content
		dir := filepath.Join("submissions", fmt.Sprintf("%d/%d/1", inject.ID, team.ID))
		err = os.MkdirAll(dir, 0750)
		require.NoError(t, err)

		content := fmt.Sprintf("Report from team %d", team.ID)
		err = os.WriteFile(filepath.Join(dir, "report.pdf"), []byte(content), 0644)
		require.NoError(t, err)

		_, err = db.CreateSubmission(db.SubmissionSchema{
			TeamID:             team.ID,
			InjectID:           inject.ID,
			SubmissionTime:     time.Now(),
			SubmissionFileName: "report.pdf",
		})
		require.NoError(t, err)
	}

	// Make request
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/injects/%d/submissions/download", inject.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", inject.ID))
	rr := httptest.NewRecorder()

	api.DownloadAllSubmissions(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Logf("Response body: %s", rr.Body.String())
	}
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/zip", rr.Header().Get("Content-Type"))

	// Verify ZIP contains all 3 files (not overwritten)
	zipReader, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	require.NoError(t, err)
	require.Len(t, zipReader.File, numTeams, "ZIP should contain one file per team")

	// Verify each file has unique content from different teams
	contents := make(map[string]bool)
	for _, f := range zipReader.File {
		t.Logf("ZIP entry: %s", f.Name)
		rc, _ := f.Open()
		content, _ := io.ReadAll(rc)
		rc.Close()
		contents[string(content)] = true
	}

	// All 3 team contents should be unique
	assert.Len(t, contents, numTeams, "All team submissions should have unique content in ZIP")
}
