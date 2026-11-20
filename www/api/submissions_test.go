package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func submissionAPI(t *testing.T) (*API, db.TeamSchema) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	t.Chdir(t.TempDir()) // the handler writes under ./submissions
	require.NoError(t, os.Mkdir("submissions", 0o750))
	rdb, pg := testutil.StartContainers(t)
	eng := engine.NewTestEngine(t, rdb, pg, 3)
	return NewAPI(eng.Config, eng), pg.CreateTestTeam(t, "Submission Team", "01")
}

// submissionRequest posts a file for injectID as a team user; mid, when set,
// is read between the two halves of the body (a slow upload).
func submissionRequest(t *testing.T, team db.TeamSchema, injectID uint, mid io.Reader) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "report.pdf")
	require.NoError(t, err)
	_, err = fw.Write(bytes.Repeat([]byte("x"), 64<<10))
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	raw := body.Bytes()
	var r io.Reader = bytes.NewReader(raw)
	if mid != nil {
		r = io.MultiReader(bytes.NewReader(raw[:len(raw)/2]), mid, bytes.NewReader(raw[len(raw)/2:]))
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/injects/%d/submit", injectID), r)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("id", fmt.Sprint(injectID))
	return req.WithContext(WithIdentity(context.Background(), Identity{Username: "blue", Roles: []string{"team"}, TeamID: team.ID, HasTeam: true}))
}

// slowReader blocks once, like a large upload on a slow team link.
type slowReader struct{ done bool }

func (s *slowReader) Read([]byte) (int, error) {
	if !s.done {
		time.Sleep(time.Second)
		s.done = true
	}
	return 0, io.EOF
}

// An upload that reached the server before CloseTime is on time even if it
// finishes after it.
func TestCreateSubmissionTimedByArrival(t *testing.T) {
	a, team := submissionAPI(t)
	now := time.Now()
	inject, err := a.eng.DB.CreateInject(db.InjectSchema{
		Title: "Closing soon", Description: "d",
		OpenTime: now.Add(-time.Hour), DueTime: now.Add(500 * time.Millisecond), CloseTime: now.Add(500 * time.Millisecond),
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	a.CreateSubmission(rec, submissionRequest(t, team, inject.ID, &slowReader{}))
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// A team cannot submit to an inject that has not opened yet (GetInjects hides
// it), nor to one that does not exist.
func TestCreateSubmissionRejectsUnopenedAndUnknownInjects(t *testing.T) {
	a, team := submissionAPI(t)
	now := time.Now()
	inject, err := a.eng.DB.CreateInject(db.InjectSchema{
		Title: "Later", Description: "d",
		OpenTime: now.Add(time.Hour), DueTime: now.Add(2 * time.Hour), CloseTime: now.Add(3 * time.Hour),
	})
	require.NoError(t, err)

	for _, id := range []uint{inject.ID, inject.ID + 1000} {
		rec := httptest.NewRecorder()
		a.CreateSubmission(rec, submissionRequest(t, team, id, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, "inject %d: %s", id, rec.Body.String())
	}
	subs, err := a.eng.DB.GetSubmissionsForInject(inject.ID)
	require.NoError(t, err)
	assert.Empty(t, subs)
}
