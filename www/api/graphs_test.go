package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/stretchr/testify/assert"
)

func withGraphFlags(t *testing.T, publicGraphs bool, disableForBlue bool) *API {
	t.Helper()
	return &API{conf: &config.ConfigSettings{
		UISettings: config.UIConfig{
			EnablePublicGraphs:       publicGraphs,
			DisableGraphsForBlueTeam: disableForBlue,
		},
	}}
}

var graphHandlers = []struct {
	name    string
	handler func(*API) http.HandlerFunc
}{
	{"GetServiceStatus", func(a *API) http.HandlerFunc { return a.GetServiceStatus }},
	{"GetScoreStatus", func(a *API) http.HandlerFunc { return a.GetScoreStatus }},
	{"GetUptimeStatus", func(a *API) http.HandlerFunc { return a.GetUptimeStatus }},
}

// TestGraphsPublicDisabled: anonymous callers get 403 when EnablePublicGraphs=false.
func TestGraphsPublicDisabled(t *testing.T) {
	a := withGraphFlags(t, false, false)
	anon := Identity{}

	for _, h := range graphHandlers {
		t.Run(h.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := requestAs(nil, anon)
			h.handler(a)(w, r)
			assert.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

// TestGraphsPublicDisabledAdminPasses: admin is not blocked by the flag gate.
func TestGraphsPublicDisabledAdminPasses(t *testing.T) {
	a := withGraphFlags(t, false, false)
	admin := Identity{Username: "admin", Roles: []string{"admin"}}

	for _, h := range graphHandlers {
		t.Run(h.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := requestAs([]string{"admin"}, admin)
			assert.True(t, a.checkGraphAccess(w, r), "admin must pass the flag gate")
		})
	}
}

// TestGraphsDisableForBlueTeam: team-role callers get 403 when DisableGraphsForBlueTeam=true.
func TestGraphsDisableForBlueTeam(t *testing.T) {
	a := withGraphFlags(t, true, true)
	teamUser := Identity{Username: "team01", Roles: []string{"team"}, TeamID: 1, HasTeam: true}

	for _, h := range graphHandlers {
		t.Run(h.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := requestAs([]string{"team"}, teamUser)
			h.handler(a)(w, r)
			assert.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

// TestGraphsDisableForBlueTeamAdminPasses: admin passes even when DisableGraphsForBlueTeam=true.
func TestGraphsDisableForBlueTeamAdminPasses(t *testing.T) {
	a := withGraphFlags(t, true, true)
	admin := Identity{Username: "admin", Roles: []string{"admin"}}

	for _, h := range graphHandlers {
		t.Run(h.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := requestAs([]string{"admin"}, admin)
			assert.True(t, a.checkGraphAccess(w, r), "admin must pass even when blue team is disabled")
		})
	}
}

// TestGraphsDefaultFlagsNoExtraGate: with both flags false, the flag gate does not fire for
// non-anonymous, non-team callers.
func TestGraphsDefaultFlagsNoExtraGate(t *testing.T) {
	a := withGraphFlags(t, false, false)
	inject := Identity{Username: "injectmgr", Roles: []string{"inject"}}

	for _, h := range graphHandlers {
		t.Run(h.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := requestAs([]string{"inject"}, inject)
			passed := a.checkGraphAccess(w, r)
			assert.True(t, passed, "inject role must pass flag gate with default flags")
			assert.NotContains(t, w.Body.String(), "not publicly accessible")
			assert.NotContains(t, w.Body.String(), "disabled for team users")
		})
	}
}
