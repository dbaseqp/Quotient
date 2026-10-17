package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadMiscSettings(t *testing.T, misc string) MiscConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.conf")
	content := `[RequiredSettings]
EventName = "Test"
EventType = "rvb"
DBConnectURL = "postgres://test:test@localhost:5432/test"
BindAddress = "0.0.0.0:8080"

[MiscSettings]
` + misc
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	var conf ConfigSettings
	require.NoError(t, conf.SetConfig(path))
	return conf.MiscSettings
}

func TestSlaPenaltyExplicitZeroIsKept(t *testing.T) {
	misc := loadMiscSettings(t, "Points = 2\nSlaThreshold = 4\nSlaPenalty = 0\n")
	assert.Equal(t, 0, misc.SlaPenalty)
	assert.Equal(t, 8, misc.HypotheticalSlaPenalty())
}

func TestSlaPenaltyOmittedUsesDefault(t *testing.T) {
	misc := loadMiscSettings(t, "Points = 2\nSlaThreshold = 4\n")
	assert.Equal(t, 8, misc.SlaPenalty)
	assert.Equal(t, 8, misc.HypotheticalSlaPenalty())
}

func TestSlaPenaltyExplicitValueIsKept(t *testing.T) {
	misc := loadMiscSettings(t, "Points = 2\nSlaThreshold = 4\nSlaPenalty = 3\n")
	assert.Equal(t, 3, misc.SlaPenalty)
	assert.Equal(t, 3, misc.HypotheticalSlaPenalty())
}

func TestSlaPenaltyNegativeIsRejected(t *testing.T) {
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{SlaPenalty: -5},
	}
	require.Error(t, checkConfig(conf))
}
