package engine

import (
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A database error while Start reads the stored rounds must delay scoring, not
// start it from round 0, whose writes would all collide with stored rounds.
func TestStart_WaitsForRoundStateAfterLoadError(t *testing.T) {
	redis, pg := testutil.StartContainers(t)
	require.NoError(t, pg.DB.ResetScores())
	for id := uint(1); id <= 3; id++ {
		_, err := pg.DB.CreateRound(db.RoundSchema{ID: id, StartTime: time.Now()})
		require.NoError(t, err)
	}

	se := NewTestEngine(t, redis, pg, 3)
	se.CurrentRound = 0 // a freshly started process

	pg.Exec(t, "ALTER TABLE round_schemas RENAME TO round_schemas_away")
	restored := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-restored:
		default:
			pg.Exec(t, "ALTER TABLE round_schemas_away RENAME TO round_schemas")
		}
	})

	firstRound := make(chan uint, 1)
	se.roundFunc = func() error {
		firstRound <- se.CurrentRound
		return errReset
	}
	done := make(chan struct{})
	go func() { defer close(done); se.Start() }()

	time.Sleep(time.Second) // Start is retrying against the missing table
	pg.Exec(t, "ALTER TABLE round_schemas_away RENAME TO round_schemas")
	close(restored)

	select {
	case round := <-firstRound:
		assert.Equal(t, uint(4), round, "scoring resumes after the stored rounds")
	case <-time.After(15 * time.Second):
		t.Fatal("no round ran after the database recovered")
	}

	require.NoError(t, se.ResetScores())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after reset")
	}
}
