package config

import (
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpreadDefaultsToShortestRound(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 2, 45, checks.Service{})
	require.NoError(t, checkConfig(conf))
	require.NotNil(t, conf.MiscSettings.Spread)
	assert.Equal(t, 110, *conf.MiscSettings.Spread)
}

func TestSpreadExplicitValuesKept(t *testing.T) {
	for _, v := range []int{0, 30, 500} {
		conf := attemptTestConfig(120, 10, 30, 2, 45, checks.Service{})
		conf.MiscSettings.Spread = &v
		require.NoError(t, checkConfig(conf))
		assert.Equal(t, v, *conf.MiscSettings.Spread)
	}
}

func TestNegativeSpreadRejected(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 2, 45, checks.Service{})
	v := -1
	conf.MiscSettings.Spread = &v
	require.ErrorContains(t, checkConfig(conf), "spread must not be negative")
}
