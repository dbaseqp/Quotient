package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestPropertyConfigTimingConstraints verifies timing constraints always hold after validation
func TestPropertyConfigTimingConstraints(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random valid timing values that satisfy constraints
		delay := rapid.IntRange(20, 300).Draw(t, "delay")       // Start at 20 to ensure room for jitter and timeout
		jitter := rapid.IntRange(1, delay/2).Draw(t, "jitter")  // Valid: jitter < delay, max half of delay
		timeout := rapid.IntRange(1, delay-jitter-1).Draw(t, "timeout") // Valid: timeout < delay - jitter

		conf := &ConfigSettings{
			RequiredSettings: RequiredConfig{
				EventName:    "Test Event",
				EventType:    "rvb",
				DBConnectURL: "postgres://test:test@localhost:5432/test",
				BindAddress:  "0.0.0.0:8080",
			},
			MiscSettings: MiscConfig{
				Delay:   delay,
				Jitter:  jitter,
				Timeout: timeout,
			},
		}

		// CALL REAL ENGINE CODE
		err := checkConfig(conf)

		// Property: Valid config should pass validation
		assert.NoError(t, err, "valid config should pass validation")

		// Property 1: Jitter must be < Delay (line 283-285)
		assert.Less(t, conf.MiscSettings.Jitter, conf.MiscSettings.Delay,
			"jitter must be < delay")

		// Property 2: Timeout must be < Delay - Jitter (line 290-292)
		assert.Less(t, conf.MiscSettings.Timeout,
			conf.MiscSettings.Delay-conf.MiscSettings.Jitter,
			"timeout must be < delay - jitter")

		// Property 3: SLA penalty defaults correctly (line 303)
		if conf.MiscSettings.SlaPenalty == 0 {
			// If not set, checkConfig sets default
			expectedPenalty := conf.MiscSettings.SlaThreshold * conf.MiscSettings.Points
			assert.Equal(t, expectedPenalty, conf.MiscSettings.SlaPenalty,
				"SLA penalty should default to threshold * points")
		}

		// Property 4: Points has sensible default (line 294-296)
		assert.GreaterOrEqual(t, conf.MiscSettings.Points, 1,
			"points should be >= 1 after validation")

		// Property 5: SLA threshold has sensible default (line 298-300)
		assert.GreaterOrEqual(t, conf.MiscSettings.SlaThreshold, 1,
			"SLA threshold should be >= 1 after validation")
	})
}

// TestPropertyConfigRejectsInvalidJitter verifies jitter >= delay is rejected
func TestPropertyConfigRejectsInvalidJitter(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		delay := rapid.IntRange(1, 100).Draw(t, "delay")
		jitter := rapid.IntRange(delay, delay+50).Draw(t, "jitter") // INVALID: jitter >= delay

		conf := &ConfigSettings{
			RequiredSettings: RequiredConfig{
				EventName:    "Test Event",
				EventType:    "rvb",
				DBConnectURL: "postgres://test:test@localhost:5432/test",
				BindAddress:  "0.0.0.0:8080",
			},
			MiscSettings: MiscConfig{
				Delay:  delay,
				Jitter: jitter,
			},
		}

		err := checkConfig(conf)

		// Property: Invalid jitter MUST be rejected (line 283-285)
		require.Error(t, err, "jitter >= delay should fail validation")
		assert.Contains(t, err.Error(), "jitter must be smaller than delay",
			"error should mention jitter constraint")
	})
}

// TestPropertyConfigRejectsInvalidTimeout verifies timeout >= delay-jitter is rejected
func TestPropertyConfigRejectsInvalidTimeout(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		delay := rapid.IntRange(20, 100).Draw(t, "delay")
		jitter := rapid.IntRange(1, delay/2).Draw(t, "jitter") // Valid jitter
		// INVALID timeout: >= delay - jitter
		timeout := rapid.IntRange(delay-jitter, delay).Draw(t, "timeout")

		conf := &ConfigSettings{
			RequiredSettings: RequiredConfig{
				EventName:    "Test Event",
				EventType:    "rvb",
				DBConnectURL: "postgres://test:test@localhost:5432/test",
				BindAddress:  "0.0.0.0:8080",
			},
			MiscSettings: MiscConfig{
				Delay:   delay,
				Jitter:  jitter,
				Timeout: timeout,
			},
		}

		err := checkConfig(conf)

		// Property: Invalid timeout MUST be rejected (line 290-292)
		require.Error(t, err, "timeout >= delay-jitter should fail validation")
		assert.Contains(t, err.Error(), "timeout must be smaller than delay minus jitter",
			"error should mention timeout constraint")
	})
}

// TestPropertyConfigPortDefaults verifies port defaults based on SSL configuration
func TestPropertyConfigPortDefaults(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hasSSL := rapid.Bool().Draw(t, "hasSSL")

		conf := &ConfigSettings{
			RequiredSettings: RequiredConfig{
				EventName:    "Test Event",
				EventType:    "rvb",
				DBConnectURL: "postgres://test:test@localhost:5432/test",
				BindAddress:  "0.0.0.0:8080",
			},
			MiscSettings: MiscConfig{
				Delay:  60,
				Jitter: 5,
				// Port intentionally 0 (should be set by checkConfig)
			},
		}

		if hasSSL {
			// Set SSL config (requires cert and key)
			conf.SslSettings = SslConfig{
				HttpsCert: "/path/to/cert.pem",
				HttpsKey:  "/path/to/key.pem",
			}
		}

		err := checkConfig(conf)

		if hasSSL {
			// With SSL, should default to port 443 (line 275-277)
			assert.NoError(t, err)
			assert.Equal(t, 443, conf.MiscSettings.Port,
				"with SSL, port should default to 443")
		} else {
			// Without SSL, should default to port 80 (line 278)
			assert.NoError(t, err)
			assert.Equal(t, 80, conf.MiscSettings.Port,
				"without SSL, port should default to 80")
		}
	})
}

// TestPropertyConfigCredlistPathNormalization verifies credlist paths are normalized
func TestPropertyConfigCredlistPathNormalization(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random credlist paths with or without directories
		testPath := rapid.SampledFrom([]string{
			"/path/to/creds.csv",
			"./local/users.csv",
			"../parent/passwords.csv",
			"simple.csv",
		}).Draw(t, "path")

		conf := &ConfigSettings{
			RequiredSettings: RequiredConfig{
				EventName:    "Test Event",
				EventType:    "rvb",
				DBConnectURL: "postgres://test:test@localhost:5432/test",
				BindAddress:  "0.0.0.0:8080",
			},
			MiscSettings: MiscConfig{
				Delay:  60,
				Jitter: 5,
			},
			CredlistSettings: CredlistConfig{
				Credlist: []Credlist{
					{
						CredlistPath: testPath,
					},
				},
			},
		}

		err := checkConfig(conf)
		assert.NoError(t, err)

		// Property: All credlist paths should be normalized to basename (line 253)
		for _, credlist := range conf.CredlistSettings.Credlist {
			assert.NotContains(t, credlist.CredlistPath, "/",
				"credlist path should not contain directory separators after validation")
			// Path should be just the filename, no directories
			assert.Contains(t, []string{"creds.csv", "users.csv", "passwords.csv", "simple.csv"},
				credlist.CredlistPath,
				"path should be normalized to basename only")
		}
	})
}
