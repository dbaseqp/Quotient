package config

import (
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
)

func TestWorkstationNameTemplatesValidated(t *testing.T) {
	for _, bad := range []string{"", "WS _-01", "WS_\t"} {
		conf := attemptTestConfig(120, 10, 30, 1, 0, checks.Service{})
		conf.MiscSettings.WorkstationNames = []string{"WS_-01", bad}
		assert.Error(t, checkConfig(conf), "template %q", bad)
	}
}
