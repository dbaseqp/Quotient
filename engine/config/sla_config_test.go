package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dbaseqp/Quotient/engine/checks"
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
	assert.Equal(t, 0, *misc.SlaPenalty)
}

func TestSlaPenaltyOmittedUsesDefault(t *testing.T) {
	misc := loadMiscSettings(t, "Points = 2\nSlaThreshold = 4\n")
	assert.Equal(t, 8, *misc.SlaPenalty)
}

func TestSlaPenaltyExplicitValueIsKept(t *testing.T) {
	misc := loadMiscSettings(t, "Points = 2\nSlaThreshold = 4\nSlaPenalty = 3\n")
	assert.Equal(t, 3, *misc.SlaPenalty)
}

func TestSlaPenaltyNegativeIsRejected(t *testing.T) {
	penalty := -5
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{SlaPenalty: &penalty},
	}
	require.Error(t, checkConfig(conf))
}

// The legacy graph flag used to share an else-if chain with the SlaPenalty
// checks, so setting it let a negative penalty (an SLA bonus) through.
func TestSlaPenaltyNegativeIsRejectedWithLegacyGraphFlag(t *testing.T) {
	penalty := -5
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{SlaPenalty: &penalty},
		UISettings:   UIConfig{AllowNonAnonymizedGraphsForBlueTeam: true},
	}
	err := checkConfig(conf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SlaPenalty must not be negative")
	assert.True(t, conf.UISettings.AllowNonAnonymizedGraphs, "legacy flag still maps to the new one")
}

func slaOverrideConfig(services ...checks.Service) *ConfigSettings {
	penalty := 30
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{
			EventName:    "Test",
			EventType:    "rvb",
			DBConnectURL: "postgres://test:test@localhost:5432/test",
			BindAddress:  "0.0.0.0:8080",
		},
		MiscSettings: MiscConfig{SlaThreshold: 5, SlaPenalty: &penalty},
		Box:          []Box{{Name: "box", IP: "192.0.2.1"}},
	}
	for _, svc := range services {
		svc.Port = 22
		conf.Box[0].Tcp = append(conf.Box[0].Tcp, &checks.Tcp{Service: svc})
	}
	return conf
}

// A check's SlaThreshold and SlaPenalty override MiscSettings'; unset values,
// and checks the config no longer has, get MiscSettings'.
func TestSlaRulesPerCheckOverrides(t *testing.T) {
	seven, zero := 7, 0
	conf := slaOverrideConfig(
		checks.Service{Display: "strict", SlaThreshold: 2, SlaPenalty: &seven},
		checks.Service{Display: "plain"},
		checks.Service{Display: "free", SlaPenalty: &zero},
		checks.Service{Display: "patient", SlaThreshold: 8},
	)
	require.NoError(t, checkConfig(conf))

	rules := conf.SlaRules()
	assert.Equal(t, SlaRule{Threshold: 2, Penalty: 7}, rules("box-strict"))
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 30}, rules("box-plain"))
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 0}, rules("box-free"), "an explicit per-check 0 deducts nothing")
	assert.Equal(t, SlaRule{Threshold: 8, Penalty: 30}, rules("box-patient"))
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 30}, rules("box-removed"), "a check a reload removed gets the globals")
}

// Unset per-check values inherit even when the check never went through
// checkConfig.
func TestSlaRulesUnconfiguredCheckInherits(t *testing.T) {
	conf := slaOverrideConfig(checks.Service{Name: "box-raw"})
	conf.Box[0].Runners = []checks.Runner{conf.Box[0].Tcp[0]}
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 30}, conf.SlaRules()("box-raw"))
}

func TestSlaPerCheckNegativeIsRejected(t *testing.T) {
	minusOne := -1
	for name, svc := range map[string]checks.Service{
		"threshold": {SlaThreshold: -1},
		"penalty":   {SlaPenalty: &minusOne},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkConfig(slaOverrideConfig(svc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "box-tcp: SlaThreshold and SlaPenalty must not be negative")
		})
	}
}

// slapenalty = 0 on a check is kept as an explicit 0, not replaced by the
// global penalty.
func TestSlaPerCheckExplicitZeroPenaltyLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.conf")
	content := `[RequiredSettings]
EventName = "Test"
EventType = "rvb"
DBConnectURL = "postgres://test:test@localhost:5432/test"
BindAddress = "0.0.0.0:8080"

[MiscSettings]
SlaThreshold = 5
SlaPenalty = 30

[[Box]]
Name = "box"
IP = "192.0.2.1"

[[Box.Tcp]]
Display = "free"
Port = 22
SlaPenalty = 0

[[Box.Tcp]]
Display = "plain"
Port = 23
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	var conf ConfigSettings
	require.NoError(t, conf.SetConfig(path))

	rules := conf.SlaRules()
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 0}, rules("box-free"))
	assert.Equal(t, SlaRule{Threshold: 5, Penalty: 30}, rules("box-plain"))
}
