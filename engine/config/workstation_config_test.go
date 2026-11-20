package config

import (
	"encoding/json"
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkstationNameTemplatesValidated(t *testing.T) {
	for _, bad := range []string{"", "WS _-01", "WS_\t"} {
		conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
		conf.MiscSettings.WorkstationNames = []string{"WS_-01", bad}
		assert.Error(t, checkConfig(conf), "template %q", bad)
	}
}

// workstationSeeds loads a config with the given WorkstationSeed and returns the pool seed that
// each check sending a client name (SMB, SMTP) carries to its runner.
func workstationSeeds(t *testing.T, seed string) []string {
	t.Helper()
	conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
	svc := conf.Box[0].Tcp[0].Service
	conf.Box[0].Smb = []*checks.Smb{{Service: svc, Share: "share"}}
	conf.Box[0].Smtp = []*checks.Smtp{{Service: svc}}
	conf.MiscSettings.WorkstationSeed = seed
	require.NoError(t, checkConfig(conf))

	var seeds []string
	for _, r := range conf.Box[0].Runners {
		data, err := json.Marshal(r)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(data, &fields))
		if s, ok := fields["WorkstationSeed"].(string); ok {
			seeds = append(seeds, s)
		}
	}
	require.NotEmpty(t, seeds, "no check carries a workstation seed")
	return seeds
}

func TestWorkstationSeedReachesTheRunners(t *testing.T) {
	seeds := workstationSeeds(t, "s3cret")
	for _, s := range seeds {
		assert.Equal(t, "s3cret", s, "the configured seed builds the pools")
	}
	assert.Equal(t, seeds, workstationSeeds(t, "s3cret"), "same seed, same pools on every config load")
	assert.NotEqual(t, seeds, workstationSeeds(t, "other"), "the pools follow the seed, not the event name")

	random := workstationSeeds(t, "")
	for _, s := range random {
		assert.NotEqual(t, "Test Event", s, "the event name is public: it must not seed the pools")
		assert.Len(t, s, 64, "no seed configured: a random one")
	}
	assert.Equal(t, random, workstationSeeds(t, ""), "the random seed is stable within the process")
}
