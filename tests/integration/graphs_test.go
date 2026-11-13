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
	pg.ResetCompetitionState(t)
	eng := engine.NewTestEngine(t, redis, pg, 3)
	a := api.NewAPI(eng.Config, eng)
	require.NoError(t, pg.DB.SetCompetitionStarted(true))
	t.Cleanup(func() { assert.NoError(t, pg.DB.ResetScores()) })

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

func TestPreflightWebUIEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	pg.ResetCompetitionState(t)
	t.Cleanup(func() { assert.NoError(t, pg.DB.ResetScores()) })

	eng := engine.NewTestEngine(t, redis, pg, 3)
	a := api.NewAPI(eng.Config, eng)

	team, err := pg.DB.CreateTeam(db.TeamSchema{
		Name:       fmt.Sprintf("preflight-ui-%d", time.Now().UnixNano()),
		Identifier: "01",
		Active:     true,
	})
	require.NoError(t, err)

	roundTime := time.Now().Truncate(time.Microsecond)
	require.NoError(t, pg.DB.CreatePreflightRound(db.PreflightRoundSchema{
		ID:        1,
		StartTime: roundTime,
		Checks: []db.PreflightCheckSchema{
			{
				TeamID:      team.ID,
				ServiceName: "box01-ssh",
				ServiceType: "SSH",
				Result:      true,
				Debug:       "creds used were admin:secret",
			},
			{
				TeamID:      team.ID,
				ServiceName: "box01-web",
				ServiceType: "Web",
				Result:      false,
				Error:       "connection refused",
			},
		},
	}))

	adminID := &api.Identity{Username: "admin", Roles: []string{"admin"}}
	teamUserID := &api.Identity{Username: "team01", Roles: []string{"team"}, TeamID: team.ID, HasTeam: true}

	callWithIdentity := func(method, target string, pathValues map[string]string, id *api.Identity, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		for k, v := range pathValues {
			r.SetPathValue(k, v)
		}
		if id != nil {
			ctx := api.WithIdentity(r.Context(), *id)
			ctx = context.WithValue(ctx, "roles", id.Roles) //nolint:staticcheck
			r = r.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}

	// Non-admins are blocked during preflight.
	for _, h := range []http.HandlerFunc{a.GetServiceStatus, a.GetUptimeStatus, a.GetTeamSummary, a.GetServiceAll} {
		w := callWithIdentity(http.MethodGet, "/", map[string]string{"team_id": fmt.Sprint(team.ID), "service_name": "box01-ssh"}, teamUserID, h)
		assert.Equal(t, http.StatusForbidden, w.Code)
	}

	// Admin sees preflight service status on /api/graphs/services.
	w := callWithIdentity(http.MethodGet, "/api/graphs/services", nil, adminID, a.GetServiceStatus)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var statusResp struct {
		RoundID uint `json:"roundID"`
		Series  []struct {
			ID   uint
			Name string
			Data []struct {
				X string
				Y int
			}
		} `json:"series"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &statusResp))
	assert.Equal(t, uint(1), statusResp.RoundID)
	var foundTeamStatus bool
	for _, s := range statusResp.Series {
		if s.ID == team.ID {
			foundTeamStatus = true
			byService := map[string]int{}
			for _, pt := range s.Data {
				byService[pt.X] = pt.Y
			}
			assert.Equal(t, api.Up, byService["box01-ssh"])
			assert.Equal(t, api.Down, byService["box01-web"])
		}
	}
	assert.True(t, foundTeamStatus, "preflight service status includes active team")

	// Admin sees preflight uptimes on /api/graphs/uptimes.
	w = callWithIdentity(http.MethodGet, "/api/graphs/uptimes", nil, adminID, a.GetUptimeStatus)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var uptimeResp struct {
		Series []struct {
			Name string
			Data []struct {
				Service string
				Uptime  float64
			}
		} `json:"series"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &uptimeResp))
	var foundTeamUptime bool
	for _, s := range uptimeResp.Series {
		if s.Name == team.Name {
			foundTeamUptime = true
			byService := map[string]float64{}
			for _, pt := range s.Data {
				byService[pt.Service] = pt.Uptime
			}
			assert.InDelta(t, 1.0, byService["box01-ssh"], 1e-6)
			assert.InDelta(t, 0.0, byService["box01-web"], 1e-6)
		}
	}
	assert.True(t, foundTeamUptime, "preflight uptimes include active team")

	// Admin sees preflight team summary on /api/services/{team_id}.
	w = callWithIdentity(http.MethodGet, "/api/services/"+fmt.Sprint(team.ID), map[string]string{"team_id": fmt.Sprint(team.ID)}, adminID, a.GetTeamSummary)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var summaries []struct {
		ServiceName  string
		SlaCount     int
		Last10Rounds []db.RoundSchema
		Uptime       float64
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summaries))
	require.Len(t, summaries, 2)
	assert.Equal(t, "box01-ssh", summaries[0].ServiceName)
	assert.InDelta(t, 1.0, summaries[0].Uptime, 1e-6)
	require.Len(t, summaries[0].Last10Rounds, 1)
	assert.Equal(t, uint(1), summaries[0].Last10Rounds[0].ID)
	assert.Equal(t, "box01-web", summaries[1].ServiceName)
	assert.InDelta(t, 0.0, summaries[1].Uptime, 1e-6)

	// Admin sees preflight drilldown on /api/services/{team_id}/{service_name}.
	w = callWithIdentity(http.MethodGet, "/api/services/"+fmt.Sprint(team.ID)+"/box01-ssh", map[string]string{"team_id": fmt.Sprint(team.ID), "service_name": "box01-ssh"}, adminID, a.GetServiceAll)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var drilldown []db.ServiceCheckSchema
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &drilldown))
	require.Len(t, drilldown, 1)
	assert.Equal(t, uint(1), drilldown[0].Round.ID)
	assert.True(t, drilldown[0].Result)
	assert.Equal(t, "creds used were admin:secret", drilldown[0].Debug)

	// Admin sees preflight round in /api/engine.
	w = callWithIdentity(http.MethodGet, "/api/engine", nil, adminID, a.GetEngine)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var engineResp struct {
		Preflight bool           `json:"preflight"`
		LastRound db.RoundSchema `json:"last_round"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &engineResp))
	assert.True(t, engineResp.Preflight)
	assert.Equal(t, uint(1), engineResp.LastRound.ID)

	// Starting the competition immediately stops serving preflight data even before scored round 1 finishes.
	require.NoError(t, pg.DB.SetCompetitionStarted(true))

	w = callWithIdentity(http.MethodGet, "/api/graphs/services", nil, teamUserID, a.GetServiceStatus)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &statusResp))
	assert.Zero(t, statusResp.RoundID, "preflight round must not leak once competition starts")

	w = callWithIdentity(http.MethodGet, "/api/services/"+fmt.Sprint(team.ID), map[string]string{"team_id": fmt.Sprint(team.ID)}, teamUserID, a.GetTeamSummary)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	summaries = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summaries))
	assert.Empty(t, summaries, "preflight services must not leak to team after competition starts")

	w = callWithIdentity(http.MethodGet, "/api/engine", nil, adminID, a.GetEngine)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &engineResp))
	assert.False(t, engineResp.Preflight)
	assert.Zero(t, engineResp.LastRound.ID)
}
