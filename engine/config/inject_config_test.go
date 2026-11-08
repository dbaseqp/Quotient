package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompetitionStartUsesNamedTimezone(t *testing.T) {
	conf := ConfigSettings{InjectSettings: InjectConfig{
		CompetitionStartTime: "2027-03-19 08:00",
		CompetitionTimezone:  "America/Los_Angeles",
	}}

	start, err := conf.CompetitionStart()
	require.NoError(t, err)
	assert.Equal(t, time.Date(2027, time.March, 19, 15, 0, 0, 0, time.UTC), start.UTC())
}

func TestCompetitionStartRejectsIncompleteSettings(t *testing.T) {
	conf := ConfigSettings{InjectSettings: InjectConfig{CompetitionStartTime: "2027-03-19 08:00"}}
	_, err := conf.CompetitionStart()
	require.Error(t, err)
}

func TestCompetitionStartRejectsNonexistentLocalTime(t *testing.T) {
	conf := ConfigSettings{InjectSettings: InjectConfig{
		CompetitionStartTime: "2027-03-14 02:30",
		CompetitionTimezone:  "America/Los_Angeles",
	}}
	_, err := conf.CompetitionStart()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestCompetitionEndUnsetIsNotConfigured(t *testing.T) {
	conf := ConfigSettings{}
	_, ok, err := conf.CompetitionEnd()
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCompetitionEndUsesNamedTimezone(t *testing.T) {
	conf := ConfigSettings{InjectSettings: InjectConfig{
		CompetitionEndTime:  "2027-03-19 16:30",
		CompetitionTimezone: "America/Los_Angeles",
	}}
	end, ok, err := conf.CompetitionEnd()
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, time.Date(2027, time.March, 19, 23, 30, 0, 0, time.UTC), end.UTC())
}

func TestCompetitionEndRequiresTimezone(t *testing.T) {
	conf := ConfigSettings{InjectSettings: InjectConfig{CompetitionEndTime: "2027-03-19 16:30"}}
	_, _, err := conf.CompetitionEnd()
	require.Error(t, err)
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

func TestCheckConfigAllowsEndWithoutStart(t *testing.T) {
	conf := &ConfigSettings{
		RequiredSettings: RequiredConfig{EventName: "Test", EventType: "rvb", DBConnectURL: "postgres://localhost/db", BindAddress: "127.0.0.1"},
		InjectSettings: InjectConfig{
			CompetitionEndTime:  "2027-03-19 16:30",
			CompetitionTimezone: "America/Los_Angeles",
		},
		Box: []Box{{Name: "box01", IP: "10.0.0.1"}},
	}
	require.NoError(t, checkConfig(conf))
}
