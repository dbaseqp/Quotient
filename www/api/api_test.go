package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeMkdirAllCreatesNestedSubdirsUnderRoot(t *testing.T) {
	submissionsDir := t.TempDir()
	subDir := filepath.Join("announcements", "42")

	require.NoError(t, SafeMkdirAll(submissionsDir, subDir, 0750))
	info, err := os.Stat(filepath.Join(submissionsDir, "announcements", "42"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Calling again when directories already exist is idempotent.
	require.NoError(t, SafeMkdirAll(submissionsDir, subDir, 0750))
}

func TestSafeMkdirAllRejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	submissionsDir := filepath.Join(tmp, "submissions")
	require.NoError(t, os.Mkdir(submissionsDir, 0750))

	err := SafeMkdirAll(submissionsDir, "../escape", 0750)
	assert.Error(t, err)
	_, statErr := os.Stat(filepath.Join(tmp, "escape"))
	assert.True(t, os.IsNotExist(statErr))
}
