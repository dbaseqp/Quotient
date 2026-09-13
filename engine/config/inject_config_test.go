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
