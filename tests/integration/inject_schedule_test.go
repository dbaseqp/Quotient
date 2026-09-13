package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportedInjectScheduleAnchorsOnceToActualStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pgContainer := testutil.StartPostgres(t)
	db.Connect(pgContainer.ConnectionString())

	openOffset := int64((30 * time.Minute) / time.Second)
	dueOffset := int64((90 * time.Minute) / time.Second)
	closeOffset := int64((2 * time.Hour) / time.Second)
	inject, err := db.CreateInject(db.InjectSchema{
		Title:       fmt.Sprintf("Relative schedule %d", time.Now().UnixNano()),
		Description: "schedule test",
		OpenTime:    time.Now().Add(24 * time.Hour),
		DueTime:     time.Now().Add(25 * time.Hour),
		CloseTime:   time.Now().Add(26 * time.Hour),
		OpenOffset:  &openOffset,
		DueOffset:   &dueOffset,
		CloseOffset: &closeOffset,
	})
	require.NoError(t, err)

	beforeStart := time.Now()
	require.NoError(t, db.SetCompetitionStarted(true))
	afterStart := time.Now()
	actualStart, err := db.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, actualStart)
	assert.False(t, actualStart.Before(beforeStart))
	assert.False(t, actualStart.After(afterStart))

	shifted, err := db.GetInjectByID(inject.ID)
	require.NoError(t, err)
	assert.Equal(t, actualStart.Add(30*time.Minute), shifted.OpenTime)
	assert.Equal(t, actualStart.Add(90*time.Minute), shifted.DueTime)
	assert.Equal(t, actualStart.Add(2*time.Hour), shifted.CloseTime)

	require.NoError(t, db.SetCompetitionStarted(false))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, db.SetCompetitionStarted(true))
	restartedAt, err := db.GetCompetitionStart()
	require.NoError(t, err)
	require.NotNil(t, restartedAt)
	assert.Equal(t, *actualStart, *restartedAt)

	afterRestart, err := db.GetInjectByID(inject.ID)
	require.NoError(t, err)
	assert.Equal(t, shifted.OpenTime, afterRestart.OpenTime)
}
