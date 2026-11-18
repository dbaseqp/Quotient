package config

import (
	"encoding/json"
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The runner only has the check definition the engine serialises, so the
// event's workstation-name templates and salt must survive that trip.
func TestWorkstationNamesReachTheRunner(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
	conf.MiscSettings.WorkstationNames = []string{"WS_-01", "LAB_-PC"}
	require.NoError(t, checkConfig(conf))

	data, err := json.Marshal(conf.Box[0].Runners[0])
	require.NoError(t, err)
	var onRunner checks.Tcp
	require.NoError(t, json.Unmarshal(data, &onRunner))
	assert.Equal(t, []string{"WS_-01", "LAB_-PC"}, onRunner.WorkstationNames)
	assert.Equal(t, "Test Event", onRunner.WorkstationSalt)
}

func TestWorkstationNameTemplatesValidated(t *testing.T) {
	for _, bad := range []string{"", "WS _-01", "WS_\t"} {
		conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
		conf.MiscSettings.WorkstationNames = []string{"WS_-01", bad}
		assert.Error(t, checkConfig(conf), "template %q", bad)
	}
}
