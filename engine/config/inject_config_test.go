package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectConfigParseUsesNamedTimezone(t *testing.T) {
	settings := InjectConfig{
		CompetitionStartTime: "2027-03-19 08:00",
		CompetitionEndTime:   "2027-03-19 16:30",
		CompetitionTimezone:  "America/Los_Angeles",
	}
	require.NoError(t, settings.parse())
	require.NotNil(t, settings.Start)
	require.NotNil(t, settings.End)
	assert.Equal(t, time.Date(2027, time.March, 19, 15, 0, 0, 0, time.UTC), settings.Start.UTC())
	assert.Equal(t, time.Date(2027, time.March, 19, 23, 30, 0, 0, time.UTC), settings.End.UTC())
}

func TestInjectConfigParseUnsetIsNil(t *testing.T) {
	settings := InjectConfig{CompetitionTimezone: "UTC"}
	require.NoError(t, settings.parse())
	assert.Nil(t, settings.Start)
	assert.Nil(t, settings.End)
}

func TestInjectConfigParseRequiresTimezone(t *testing.T) {
	for _, settings := range []InjectConfig{
		{CompetitionStartTime: "2027-03-19 08:00"},
		{CompetitionEndTime: "2027-03-19 16:30"},
	} {
		require.Error(t, settings.parse())
	}
}

func TestInjectConfigParseRejectsNonexistentLocalTime(t *testing.T) {
	settings := InjectConfig{
		CompetitionStartTime: "2027-03-14 02:30",
		CompetitionTimezone:  "America/Los_Angeles",
	}
	err := settings.parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestInjectConfigParseEndOnly(t *testing.T) {
	settings := InjectConfig{
		CompetitionEndTime:  "2027-03-19 16:30",
		CompetitionTimezone: "America/Los_Angeles",
	}
	require.NoError(t, settings.parse())
	assert.Nil(t, settings.Start)
	require.NotNil(t, settings.End)
	assert.Equal(t, time.Date(2027, time.March, 19, 23, 30, 0, 0, time.UTC), settings.End.UTC())
}

func TestInjectConfigParseClearsStaleTimes(t *testing.T) {
	end := time.Date(2027, time.March, 19, 23, 30, 0, 0, time.UTC)
	settings := InjectConfig{Start: &end, End: &end}
	require.NoError(t, settings.parse())
	assert.Nil(t, settings.Start)
	assert.Nil(t, settings.End)
}

func TestCheckConfigRejectsEndNotAfterStart(t *testing.T) {
	for _, end := range []string{"2027-03-19 08:00", "2027-03-19 07:59"} {
		conf := &ConfigSettings{InjectSettings: InjectConfig{
			CompetitionStartTime: "2027-03-19 08:00",
			CompetitionEndTime:   end,
			CompetitionTimezone:  "America/Los_Angeles",
		}}
		err := checkConfig(conf)
		require.Error(t, err, end)
		assert.Contains(t, err.Error(), "CompetitionEndTime must be after CompetitionStartTime", end)
	}
}

func TestCheckConfigRejectsInvalidEnd(t *testing.T) {
	conf := &ConfigSettings{InjectSettings: InjectConfig{
		CompetitionStartTime: "2027-03-19 08:00",
		CompetitionEndTime:   "2027-03-19 4:30pm",
		CompetitionTimezone:  "America/Los_Angeles",
	}}
	err := checkConfig(conf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid competition end time")
}

func TestCheckConfigParsesSchedule(t *testing.T) {
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{EventName: "Test", EventType: "rvb", DBConnectURL: "postgres://localhost/db", BindAddress: "127.0.0.1"},
		InjectSettings: InjectConfig{
			CompetitionEndTime:  "2027-03-19 16:30",
			CompetitionTimezone: "America/Los_Angeles",
		},
		Box: []Box{{Name: "box01", IP: "10.0.0.1"}},
	}
	require.NoError(t, checkConfig(conf))
	assert.Nil(t, conf.InjectSettings.Start)
	require.NotNil(t, conf.InjectSettings.End)
	assert.Equal(t, time.Date(2027, time.March, 19, 23, 30, 0, 0, time.UTC), conf.InjectSettings.End.UTC())
}
