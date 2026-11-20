package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalConf returns a ConfigSettings that satisfies all required-field checks
// so the only errors we see are from the duplicate-name logic.
func minimalConf() *ConfigSettings {
	penalty := 5
	return &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{
			Delay:        30,
			Jitter:       1,
			Timeout:      1,
			Points:       1,
			SlaThreshold: 1,
			SlaPenalty:   &penalty,
		},
	}
}

// TestDuplicateAccountNameAcrossRoles verifies that the same name in two role
// lists causes checkConfig to return an error.
func TestDuplicateAccountNameAcrossRoles(t *testing.T) {
	conf := minimalConf()
	conf.Admin = []Admin{{Name: "alice", Pw: "pw1"}}
	conf.Team = []Team{{Name: "alice", Pw: "pw2"}} // duplicate

	err := checkConfig(conf)
	require.Error(t, err, "duplicate account name across role lists should return error")
	assert.Contains(t, err.Error(), "alice")
}

// TestNoDuplicateAccountNames verifies that distinct names pass the check.
func TestNoDuplicateAccountNames(t *testing.T) {
	conf := minimalConf()
	conf.Admin = []Admin{{Name: "alice", Pw: "pw1"}}
	conf.Team = []Team{{Name: "bob", Pw: "pw2"}}
	conf.Red = []Red{{Name: "charlie", Pw: "pw3"}}
	conf.Inject = []Inject{{Name: "dave", Pw: "pw4"}}

	err := checkConfig(conf)
	assert.NoError(t, err, "distinct names across role lists should pass")
}
