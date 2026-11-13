package integration

import (
	"testing"

	"github.com/dbaseqp/Quotient/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeedOriginalCredentials_EmptySliceDoesNotDelete verifies that calling
// SeedOriginalCredentials with no valid rows leaves existing credentials intact.
func TestSeedOriginalCredentials_EmptySliceDoesNotDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)

	const credlist = "test-credlist-empty"

	// Pre-seed one credential via the normal code path.
	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{{"alice", "hunter2"}}))

	// Confirm it exists.
	rows, err := pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	require.Len(t, rows, 1, "pre-condition: alice must be present")

	// Now call with an empty slice — simulates a truncated/empty credlist file.
	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{}))

	// Alice must still be present — no delete should have occurred.
	rows, err = pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "credential must not be deleted when credlist file has no valid rows")
	if len(rows) > 0 {
		assert.Equal(t, "alice", rows[0].Username)
	}
}

// TestSeedOriginalCredentials_AllInvalidRowsDoesNotDelete verifies that rows
// that are malformed (not exactly 2 fields) also trigger the no-delete guard.
func TestSeedOriginalCredentials_AllInvalidRowsDoesNotDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)

	const credlist = "test-credlist-invalid"

	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{{"alice", "hunter2"}}))

	rows, err := pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	require.Len(t, rows, 1, "pre-condition: alice must be present")

	// All rows malformed — net valid usernames = 0.
	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{{"only-one-field"}, {"three", "fields", "here"}}))

	rows, err = pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "credential must not be deleted when all rows are malformed")
}

// TestSeedOriginalCredentials_ValidRowRemovesStale verifies that a credlist with
// one valid row keeps that row and removes rows whose usernames are absent —
// confirming the NOT IN delete path is unaffected.
func TestSeedOriginalCredentials_ValidRowRemovesStale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, pg := testutil.StartContainers(t)

	const credlist = "test-credlist-valid"

	// Seed both alice and bob.
	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{
		{"alice", "pw"},
		{"bob", "pw"},
	}))

	rows, err := pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	require.Len(t, rows, 2, "pre-condition: alice and bob must be present")

	// Reseed with only alice — bob should be pruned.
	require.NoError(t, pg.DB.SeedOriginalCredentials(credlist, [][]string{{"alice", "pw"}}))

	rows, err = pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only alice should remain after bob is removed from credlist")
	assert.Equal(t, "alice", rows[0].Username)

	// Also verify the original credential for bob is gone (the NOT IN delete covers both tables).
	origRows, err := pg.DB.GetOriginalCredentials(credlist)
	require.NoError(t, err)
	for _, r := range origRows {
		assert.NotEqual(t, "bob", r.Username, "bob's OriginalCredentialSchema row should be deleted")
	}
}
