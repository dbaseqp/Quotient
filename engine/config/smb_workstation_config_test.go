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
func TestWorkstationNamesReachTheSMBRunner(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
	conf.Box[0].Smb = []*checks.Smb{{Service: conf.Box[0].Tcp[0].Service, Share: "share"}}
	conf.Box[0].Tcp = nil
	conf.MiscSettings.WorkstationNames = []string{"WS_-01", "LAB_-PC"}
	require.NoError(t, checkConfig(conf))

	data, err := json.Marshal(conf.Box[0].Runners[0])
	require.NoError(t, err)
	var onRunner checks.Smb
	require.NoError(t, json.Unmarshal(data, &onRunner))
	assert.Equal(t, []string{"WS_-01", "LAB_-PC"}, onRunner.WorkstationNames)
	assert.Equal(t, "Test Event", onRunner.WorkstationSalt)
}
