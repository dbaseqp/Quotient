package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInjectTestAPI(t *testing.T) (*API, *testutil.PostgresContainer) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Chdir(t.TempDir()) // handlers write config/injects relative to cwd
	rdb, pg := testutil.StartContainers(t)
	pg.ResetCompetitionState(t)
	eng := engine.NewTestEngine(t, rdb, pg, 3)
	return NewAPI(eng.Config, eng), pg
}

func visibleInjectIDs(t *testing.T, a *API, team db.TeamSchema) []uint {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/injects", nil)
	req = req.WithContext(WithIdentity(context.Background(),
		Identity{Username: "blue", Roles: []string{"team"}, TeamID: team.ID, HasTeam: true}))
	rec := httptest.NewRecorder()
	a.GetInjects(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var injects []db.InjectSchema
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &injects))
	ids := make([]uint, 0, len(injects))
	for _, i := range injects {
		ids = append(ids, i.ID)
	}
	return ids
}

func putInject(t *testing.T, a *API, id uint, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/injects/%d", id), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("id", fmt.Sprint(id))
	req = req.WithContext(WithIdentity(context.Background(), Identity{Username: "admin", Roles: []string{"admin"}}))
	rec := httptest.NewRecorder()
	a.UpdateInject(rec, req)
	return rec
}

// TestUpdateInjectOpenTime verifies that editing an unreleased inject cannot
// move its release time into the past (which would release it to every team at
// once), while already-open injects stay editable and "release now" works.
func TestUpdateInjectOpenTime(t *testing.T) {
	a, pg := newInjectTestAPI(t)
	team := pg.CreateTestTeam(t, "inject-team", "01")

	now := time.Now().UTC().Truncate(time.Second)
	create := func(title string, open time.Time) db.InjectSchema {
		inject, err := a.eng.DB.CreateInject(db.InjectSchema{
			Title: title, Description: "scenario", OpenTime: open, DueTime: now.Add(3 * time.Hour), CloseTime: now.Add(4 * time.Hour),
		})
		require.NoError(t, err)
		return inject
	}
	fields := func(title string, open time.Time) map[string]string {
		return map[string]string{
			"title":       title + " (edited)",
			"description": "scenario",
			"open-time":   open.Format(time.RFC3339),
			"due-time":    now.Add(3 * time.Hour).Format(time.RFC3339),
			"close-time":  now.Add(4 * time.Hour).Format(time.RFC3339),
		}
	}

	t.Run("unreleased moved into the past", func(t *testing.T) {
		open := now.Add(2 * time.Hour)
		inject := create("unreleased", open)
		require.NotContains(t, visibleInjectIDs(t, a, team), inject.ID)

		// A year-less release time re-parsed by the browser lands in 2001.
		past := time.Date(2001, open.Month(), open.Day(), open.Hour(), open.Minute(), 0, 0, time.UTC)
		rec := putInject(t, a, inject.ID, fields("unreleased", past))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.NotContains(t, visibleInjectIDs(t, a, team), inject.ID, "unreleased inject must stay hidden from teams")
	})

	t.Run("unreleased released now", func(t *testing.T) {
		inject := create("release-now", now.Add(2*time.Hour))
		rec := putInject(t, a, inject.ID, fields("release-now", now.Truncate(time.Minute)))
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, visibleInjectIDs(t, a, team), inject.ID)
	})

	t.Run("already open stays editable", func(t *testing.T) {
		inject := create("open", now.Add(-2*time.Hour))
		rec := putInject(t, a, inject.ID, fields("open", now.Add(-3*time.Hour)))
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, visibleInjectIDs(t, a, team), inject.ID)
	})
}
