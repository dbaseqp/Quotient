package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"
	"github.com/dbaseqp/Quotient/www/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type graphSeries struct {
	Name string
	ID   uint
	Self bool
}

func graphRequest(t *testing.T, handler http.HandlerFunc, identity *api.Identity) (*httptest.ResponseRecorder, []graphSeries) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if identity != nil {
		ctx := api.WithIdentity(r.Context(), *identity)
		ctx = context.WithValue(ctx, "roles", identity.Roles) //nolint:staticcheck // matches the authentication middleware
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body struct{ Series []graphSeries }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w, body.Series
}

func TestGraphsMarkOnlyCallersOwnSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	eng := engine.NewTestEngine(t, redis, pg, 3)
	a := api.NewAPI(eng.Config, eng)
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	t.Cleanup(func() { pg.ResetCompetitionState(t) })

	suffix := time.Now().UnixNano()
	teams := make([]db.TeamSchema, 3)
	checks := make([]db.ServiceCheckSchema, 0, len(teams))
	for i := range teams {
		team, err := pg.DB.CreateTeam(db.TeamSchema{
			Name:       fmt.Sprintf("graphteam%d-%d", i, suffix),
			Identifier: fmt.Sprintf("%02d", i+1),
			Active:     true,
		})
		require.NoError(t, err)
		teams[i] = team
		checks = append(checks, db.ServiceCheckSchema{TeamID: team.ID, ServiceName: "box-web", Points: 1, Result: true})
	}
	_, err := pg.DB.CreateRound(db.RoundSchema{ID: 1, StartTime: time.Now(), Checks: checks})
	require.NoError(t, err)

	me := teams[1]
	handlers := map[string]http.HandlerFunc{
		"services": a.GetServiceStatus,
		"scores":   a.GetScoreStatus,
		"uptimes":  a.GetUptimeStatus,
	}

	for name, handler := range handlers {
		t.Run(name+"/own team anonymized", func(t *testing.T) {
			w, series := graphRequest(t, handler, &api.Identity{Username: "u", Roles: []string{"team"}, TeamID: me.ID, HasTeam: true})
			assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))

			selfCount := 0
			for _, s := range series {
				assert.Equal(t, "Team", s.Name)
				if s.Self {
					selfCount++
					if name == "services" {
						assert.Equal(t, me.ID, s.ID, "own row keeps its ID so it can link to the services page")
					}
				} else {
					assert.Zero(t, s.ID, "other teams' IDs must stay scrubbed")
				}
			}
			assert.Equal(t, 1, selfCount)
			for _, team := range teams {
				assert.NotContains(t, w.Body.String(), team.Name)
			}
		})

		for _, viewer := range []struct {
			label    string
			identity *api.Identity
		}{
			{"red team", &api.Identity{Username: "red", Roles: []string{"red"}}},
			{"logged out", nil},
		} {
			t.Run(name+"/"+viewer.label, func(t *testing.T) {
				w, series := graphRequest(t, handler, viewer.identity)
				for _, s := range series {
					assert.False(t, s.Self)
					assert.Zero(t, s.ID)
					assert.Equal(t, "Team", s.Name)
				}
				assert.False(t, strings.Contains(w.Body.String(), `"Self"`))
			})
		}

		t.Run(name+"/admin", func(t *testing.T) {
			_, series := graphRequest(t, handler, &api.Identity{Username: "admin", Roles: []string{"admin"}})
			names := map[string]bool{}
			for _, s := range series {
				assert.False(t, s.Self)
				names[s.Name] = true
			}
			assert.True(t, names[me.Name], "admins still see real team names")
		})
	}
}
