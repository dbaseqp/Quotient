package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"
	"github.com/dbaseqp/Quotient/www/api"

	"github.com/stretchr/testify/require"
)

// The score export lists teams in team ID order, so two exports can be diffed.
func TestExportScoresOrdersTeamsByID(t *testing.T) {
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	eng := engine.NewTestEngine(t, redis, pg, 2)
	a := api.NewAPI(eng.Config, eng)

	suffix := time.Now().UnixNano()
	for i := range 6 {
		_, err := pg.DB.CreateTeam(db.TeamSchema{Name: fmt.Sprintf("exportorder%d-%d", i, suffix), Identifier: fmt.Sprintf("%02d", i), Active: true})
		require.NoError(t, err)
	}

	for range 20 {
		w := httptest.NewRecorder()
		a.ExportScores(w, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var data []struct {
			TeamID uint `json:"team_id"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
		ids := make([]uint, len(data))
		for i, d := range data {
			ids[i] = d.TeamID
		}
		require.True(t, slices.IsSorted(ids), "export order %v", ids)
	}
}
