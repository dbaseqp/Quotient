package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredlistsDir(t *testing.T) {
	assert.Equal(t, "config/credlists", CredlistsDir(""))
	assert.Equal(t, "config/credlists", CredlistsDir("./config/event.conf"))
	assert.Equal(t, "/etc/quotient/credlists", CredlistsDir("/etc/quotient/event.conf"))
}

func TestIsReloadEvent(t *testing.T) {
	configPath := "/app/config/event.conf"
	credlistsDir := "/app/config/credlists"

	cases := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{"config write", fsnotify.Event{Name: configPath, Op: fsnotify.Write}, true},
		{"config create (atomic save)", fsnotify.Event{Name: configPath, Op: fsnotify.Create}, true},
		{"config rename", fsnotify.Event{Name: configPath, Op: fsnotify.Rename}, true},
		{"config chmod ignored", fsnotify.Event{Name: configPath, Op: fsnotify.Chmod}, false},
		{"config remove ignored", fsnotify.Event{Name: configPath, Op: fsnotify.Remove}, false},
		{"credlists dir created", fsnotify.Event{Name: credlistsDir, Op: fsnotify.Create}, true},
		{"credlist write", fsnotify.Event{Name: "/app/config/credlists/linux.credlist", Op: fsnotify.Write}, true},
		{"credlist create", fsnotify.Event{Name: "/app/config/credlists/linux.credlist", Op: fsnotify.Create}, true},
		{"credlist rename", fsnotify.Event{Name: "/app/config/credlists/linux.credlist", Op: fsnotify.Rename}, true},
		{"credlist hidden swap ignored", fsnotify.Event{Name: "/app/config/credlists/.linux.credlist.swp", Op: fsnotify.Write}, false},
		{"unrelated config dir file ignored", fsnotify.Event{Name: "/app/config/other.toml", Op: fsnotify.Write}, false},
		{"config swap file ignored", fsnotify.Event{Name: "/app/config/.event.conf.swp", Op: fsnotify.Write}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isReloadEvent(tc.event, configPath, credlistsDir))
		})
	}
}

func TestWatchConfig_ReloadsConfigAndCredlists(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "event.conf")

	writeConfig := func(eventName string) string {
		return `[RequiredSettings]
EventName = "` + eventName + `"
EventType = "rvb"
DBConnectURL = "postgres://test:test@localhost:5432/test"
BindAddress = "127.0.0.1"

[MiscSettings]
Delay = 60
Jitter = 5
`
	}

	require.NoError(t, os.WriteFile(configPath, []byte(writeConfig("Initial Event")), 0o600))

	conf := &ConfigSettings{}
	require.NoError(t, conf.SetConfig(configPath))

	reloaded := make(chan string, 4)
	require.NoError(t, conf.WatchConfig(configPath, func() {
		reloaded <- conf.RequiredSettings.EventName
	}))

	// Atomic rename replacement simulates editor in-place saves (vim/sed -i).
	tmpPath := filepath.Join(dir, ".event.conf.tmp")
	require.NoError(t, os.WriteFile(tmpPath, []byte(writeConfig("Atomic Saved Event")), 0o600))
	require.NoError(t, os.Rename(tmpPath, configPath))

	select {
	case got := <-reloaded:
		assert.Equal(t, "Atomic Saved Event", got)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for atomic config reload")
	}

	credlistsDir := filepath.Join(dir, "credlists")
	require.NoError(t, os.MkdirAll(credlistsDir, 0o750))
	credFile := filepath.Join(credlistsDir, "linux.credlist")
	require.NoError(t, os.WriteFile(credFile, []byte("alice,pass1\n"), 0o600))

	select {
	case got := <-reloaded:
		assert.Equal(t, "Atomic Saved Event", got)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for credlist creation reload")
	}

	require.NoError(t, os.WriteFile(credFile, []byte("alice,pass2\n"), 0o600))

	select {
	case got := <-reloaded:
		assert.Equal(t, "Atomic Saved Event", got)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for credlist write reload")
	}
}
