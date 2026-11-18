package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine scores every check with MiscSettings' SLA rules, so a per-check
// SlaThreshold or SlaPenalty would be silently ignored; loading must refuse it.
func TestVerifyRejectsPerCheckSlaSettings(t *testing.T) {
	for name, svc := range map[string]Service{
		"threshold": {SlaThreshold: 2},
		"penalty":   {SlaPenalty: 7},
	} {
		t.Run(name, func(t *testing.T) {
			c := &Tcp{Service: svc}
			err := c.Verify("box01", "192.0.2.1", 10, 5, 30, 5)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MiscSettings")
		})
	}

	c := &Tcp{Service: Service{Port: 22}}
	require.NoError(t, c.Verify("box01", "192.0.2.1", 10, 5, 30, 5), "checks without SLA settings still load")
}
