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

func TestReadCustomCheckScript(t *testing.T) {
	checksDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(checksDir, "acme"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(checksDir, "acme", "check.py"), []byte("print('ok')\n"), 0600))

	scriptPath, content, errStr := readCustomCheckScript(checksDir, "python3 /app/checks/acme/check.py; exit 0")
	assert.Equal(t, "/app/checks/acme/check.py", scriptPath)
	assert.Equal(t, "print('ok')\n", content)
	assert.Empty(t, errStr)

	require.NoError(t, os.WriteFile(filepath.Join(checksDir, "acme", "browser.py"), []byte("def session(): pass\n"), 0600))
	helpers := readImportedHelpers(checksDir, scriptPath, "import sys\nfrom . import browser\n")
	require.Len(t, helpers, 1)
	assert.Equal(t, "/app/checks/acme/browser.py", helpers[0].Path)
	assert.Equal(t, "def session(): pass\n", helpers[0].Content)

	scriptPath, content, errStr = readCustomCheckScript(checksDir, "python3 --script=./custom-checks/acme/check.py")
	assert.Equal(t, "./custom-checks/acme/check.py", scriptPath)
	assert.Equal(t, "print('ok')\n", content)
	assert.Empty(t, errStr)

	scriptPath, content, errStr = readCustomCheckScript(checksDir, "/app/checks/missing.sh")
	assert.Equal(t, "/app/checks/missing.sh", scriptPath)
	assert.Empty(t, content)
	assert.Contains(t, errStr, "Script file not found in ")

	scriptPath, content, errStr = readCustomCheckScript(checksDir, "/app/checks/../secret.txt")
	assert.Equal(t, "/app/checks/../secret.txt", scriptPath)
	assert.Empty(t, content)
	assert.NotEmpty(t, errStr)
	assert.NotContains(t, errStr, "Script file not found in ")
}
