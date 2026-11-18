package checks

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxChromeLag is how long a newer Chrome may have been stable before the
// version the checks claim counts as stale.
const maxChromeLag = 8 * 7 * 24 * time.Hour

// loadChromeSchedule reads the pinned milestone -> stable date schedule
// (fetched from chromiumdash; refresh it when it runs out).
func loadChromeSchedule(t *testing.T) map[int]time.Time {
	t.Helper()
	raw, err := os.ReadFile("testdata/chrome_stable_schedule.json")
	require.NoError(t, err)
	var file struct {
		Stable map[string]string `json:"stable"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	out := make(map[int]time.Time, len(file.Stable))
	for m, d := range file.Stable {
		major, err := strconv.Atoi(m)
		require.NoError(t, err)
		day, err := time.Parse(time.DateOnly, d)
		require.NoError(t, err)
		out[major] = day
	}
	return out
}

// chromeLag is how long the release after major has been stable on today
// (0 while major is the current stable). It errors when major is not stable
// yet on today, or when the schedule cannot tell (it ends before today).
func chromeLag(major int, today time.Time, stable map[int]time.Time) (time.Duration, error) {
	var last time.Time
	for _, d := range stable {
		if d.After(last) {
			last = d
		}
	}
	if last.Before(today) {
		return 0, fmt.Errorf("the schedule's last stable date is %s, before %s: refresh testdata/chrome_stable_schedule.json", last.Format(time.DateOnly), today.Format(time.DateOnly))
	}
	since, ok := stable[major]
	if !ok || since.After(today) {
		return 0, fmt.Errorf("Chrome %d is not stable yet on %s", major, today.Format(time.DateOnly))
	}
	next, ok := stable[major+1]
	if !ok || next.After(today) {
		return 0, nil
	}
	return today.Sub(next), nil
}

func TestChromeLagRule(t *testing.T) {
	stable := map[int]time.Time{}
	for m, d := range map[int]string{157: "2026-11-03", 158: "2026-11-17", 159: "2026-12-08", 160: "2027-01-05", 161: "2027-01-19", 162: "2027-02-02", 163: "2027-02-16"} {
		stable[m], _ = time.Parse(time.DateOnly, d)
	}
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }

	lag, err := chromeLag(158, day("2026-11-18"), stable)
	require.NoError(t, err)
	assert.Zero(t, lag, "158 is the current stable")

	lag, err = chromeLag(158, day("2027-02-02"), stable)
	require.NoError(t, err)
	assert.Equal(t, 56*24*time.Hour, lag, "159 has been stable 8 weeks: still allowed")
	assert.False(t, lag > maxChromeLag)

	lag, err = chromeLag(158, day("2027-02-03"), stable)
	require.NoError(t, err)
	assert.True(t, lag > maxChromeLag, "one day past 8 weeks behind")

	_, err = chromeLag(159, day("2026-11-18"), stable)
	assert.ErrorContains(t, err, "not stable yet", "a User-Agent from the future is a tell too")

	_, err = chromeLag(158, day("2027-03-01"), stable)
	assert.ErrorContains(t, err, "refresh", "past the schedule's end the test cannot tell")
}

// TestChromeMajorIsCurrent fails when ChromeMajor falls more than 8 weeks
// behind the current stable Chrome, per the pinned schedule.
func TestChromeMajorIsCurrent(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	lag, err := chromeLag(ChromeMajor, today, loadChromeSchedule(t))
	require.NoError(t, err)
	assert.LessOrEqual(t, lag, maxChromeLag,
		"ChromeMajor %d is %d days behind the current stable Chrome: bump it (and the curl_cffi target in the event checks)", ChromeMajor, int(lag.Hours()/24))
}
