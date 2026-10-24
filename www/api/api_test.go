package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeMkdirAllCreatesMissingBaseDir(t *testing.T) {
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "submissions", "announcements")

	require.NoError(t, SafeMkdirAll(baseDir, "42", 0750))
	info, err := os.Stat(filepath.Join(baseDir, "42"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Calling again when directories already exist is idempotent.
	require.NoError(t, SafeMkdirAll(baseDir, "42", 0750))
}

func TestSafeMkdirAllRejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "submissions", "announcements")

	err := SafeMkdirAll(baseDir, "../escape", 0750)
	assert.Error(t, err)
	_, statErr := os.Stat(filepath.Join(tmp, "submissions", "escape"))
	assert.True(t, os.IsNotExist(statErr))
}
