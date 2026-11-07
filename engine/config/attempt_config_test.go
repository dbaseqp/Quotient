package config

import (
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func attemptTestConfig(delay, jitter, timeout, attempts, attemptDelay int, svc checks.Service) *ConfigSettings {
	svc.Port = 80
	return &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test Event",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{
			Delay:        delay,
			Jitter:       jitter,
			Timeout:      timeout,
			Attempts:     attempts,
			AttemptDelay: attemptDelay,
		},
		Box: []Box{{
			Name: "box",
			IP:   "10.100.1_.10",
			Tcp:  []*checks.Tcp{{Service: svc}},
		}},
	}
}

// TestPropertyAttemptBudget verifies a check is accepted exactly when all its
// attempts and the delays between them fit in the shortest round.
func TestPropertyAttemptBudget(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		delay := rapid.IntRange(20, 300).Draw(t, "delay")
		jitter := rapid.IntRange(1, delay/2).Draw(t, "jitter")
		timeout := rapid.IntRange(1, delay-jitter-1).Draw(t, "timeout")
		attempts := rapid.IntRange(1, 5).Draw(t, "attempts")
		attemptDelay := rapid.IntRange(0, 200).Draw(t, "attemptDelay")

		conf := attemptTestConfig(delay, jitter, timeout, attempts, attemptDelay, checks.Service{})
		err := checkConfig(conf)

		fits := attempts*timeout+(attempts-1)*attemptDelay < delay-jitter
		if fits {
			require.NoError(t, err)
			check := conf.Box[0].Runners[0]
			assert.Equal(t, attempts, check.GetAttempts())
			assert.Equal(t, attemptDelay, check.GetAttemptDelay())
		} else {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be smaller than delay minus jitter")
		}
	})
}

func TestAttemptDefaultsDoNotOverrideService(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 2, 45, checks.Service{Attempts: 1, AttemptDelay: 5})
	require.NoError(t, checkConfig(conf))
	check := conf.Box[0].Runners[0]
	assert.Equal(t, 1, check.GetAttempts())
	assert.Equal(t, 5, check.GetAttemptDelay())
}

func TestAttemptsDefaultToOne(t *testing.T) {
	conf := attemptTestConfig(120, 10, 30, 0, 0, checks.Service{})
	require.NoError(t, checkConfig(conf))
	assert.Equal(t, 1, conf.Box[0].Runners[0].GetAttempts())
}

func TestNegativeAttemptSettingsRejected(t *testing.T) {
	require.Error(t, checkConfig(attemptTestConfig(120, 10, 30, -1, 0, checks.Service{})))
	require.Error(t, checkConfig(attemptTestConfig(120, 10, 30, 2, -1, checks.Service{})))
}
