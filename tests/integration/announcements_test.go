package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"
	"github.com/dbaseqp/Quotient/www/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadAnnouncementFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	eng := engine.NewTestEngine(t, redis, pg, 3)
	a := api.NewAPI(eng.Config, eng)
	chDirToTempForTest(t)

	writeFile := func(id any, name, content string) {
		dir := filepath.Join("submissions", "announcements", fmt.Sprint(id))
		require.NoError(t, os.MkdirAll(dir, 0750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
	}
	create := func(openTime time.Time) db.AnnouncementSchema {
		announcement, err := pg.DB.CreateAnnouncement(db.AnnouncementSchema{
			Title:                 fmt.Sprintf("Announcement-%d", time.Now().UnixNano()),
			Description:           "test",
			OpenTime:              openTime,
			AnnouncementFileNames: []string{"notes.txt"},
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pg.DB.DeleteAnnouncement(announcement)) })
		writeFile(announcement.ID, "notes.txt", fmt.Sprintf("notes %d", announcement.ID))
		return announcement
	}
	download := func(role, id, file string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/announcements/"+id+"/"+file, nil)
		req.SetPathValue("id", id)
		req.SetPathValue("file", file)
		req = req.WithContext(api.WithIdentity(req.Context(), api.Identity{Username: role, Roles: []string{role}}))
		rr := httptest.NewRecorder()
		a.DownloadAnnouncementFile(rr, req)
		return rr
	}

	open := create(time.Now().Add(-time.Hour))
	unopened := create(time.Now().Add(time.Hour))
	orphanID := fmt.Sprint(unopened.ID + 1000)
	writeFile(orphanID, "notes.txt", "orphan")

	rr := download("team", fmt.Sprint(open.ID), "notes.txt")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, fmt.Sprintf("notes %d", open.ID), rr.Body.String())

	// A zero-padded ID names the same announcement and must resolve to its
	// directory, not to one spelled like the request.
	rr = download("team", fmt.Sprintf("00%d", open.ID), "notes.txt")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, fmt.Sprintf("notes %d", open.ID), rr.Body.String())

	assert.Equal(t, http.StatusNotFound, download("team", fmt.Sprint(unopened.ID), "notes.txt").Code,
		"a team must not download from an announcement that has not opened")
	assert.Equal(t, http.StatusOK, download("admin", fmt.Sprint(unopened.ID), "notes.txt").Code)

	for _, role := range []string{"team", "admin"} {
		assert.Equal(t, http.StatusNotFound, download(role, orphanID, "notes.txt").Code,
			"%s must not download from a directory no announcement owns", role)
		assert.Equal(t, http.StatusNotFound, download(role, "0", "notes.txt").Code)
		assert.Equal(t, http.StatusBadRequest, download(role, "not-a-number", "notes.txt").Code)
	}

	// ShowAnnouncementsForRedTeam is off: the refusal must not depend on
	// whether the ID exists.
	for _, id := range []string{fmt.Sprint(open.ID), fmt.Sprint(unopened.ID), orphanID} {
		assert.Equal(t, http.StatusForbidden, download("red", id, "notes.txt").Code, "id %s", id)
	}
}
