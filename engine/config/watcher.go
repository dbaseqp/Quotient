package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// CredlistsDir returns the credlists directory sibling to configPath.
func CredlistsDir(configPath string) string {
	if configPath == "" {
		return "config/credlists"
	}
	return filepath.Join(filepath.Dir(filepath.Clean(configPath)), "credlists")
}

func setupWatcher(path string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %v", err)
	}

	configDir := filepath.Dir(path)
	if err := watcher.Add(configDir); err != nil {
		if closeErr := watcher.Close(); closeErr != nil {
			slog.Error("failed to close watcher", "error", closeErr)
		}
		return nil, fmt.Errorf("failed to watch config directory: %v", err)
	}

	credlistsDir := CredlistsDir(path)
	if info, err := os.Stat(credlistsDir); err == nil && info.IsDir() {
		if err := watcher.Add(credlistsDir); err != nil {
			slog.Warn("failed to watch credlists directory", "dir", credlistsDir, "error", err)
		}
	}

	return watcher, nil
}

func isReloadEvent(event fsnotify.Event, cleanConfigPath, cleanCredlistsDir string) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) {
		return false
	}
	cleanEvent := filepath.Clean(event.Name)
	if cleanEvent == cleanConfigPath || cleanEvent == cleanCredlistsDir {
		return true
	}
	return filepath.Dir(cleanEvent) == cleanCredlistsDir && !strings.HasPrefix(filepath.Base(cleanEvent), ".")
}

// WatchConfig watches for changes to the config file and credlists directory.
func (conf *ConfigSettings) WatchConfig(path string, onReload func()) error {
	watcher, err := setupWatcher(path)
	if err != nil {
		return err
	}

	cleanConfigPath := filepath.Clean(path)
	cleanCredlistsDir := CredlistsDir(cleanConfigPath)

	go func() {
		var (
			debounceTimer *time.Timer
			debounceCh    <-chan time.Time
		)
		defer func() {
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			if err := watcher.Close(); err != nil {
				slog.Error("failed to close watcher", "error", err)
			}
		}()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if (event.Has(fsnotify.Create) || event.Has(fsnotify.Rename)) && filepath.Clean(event.Name) == cleanCredlistsDir {
					if info, err := os.Stat(cleanCredlistsDir); err == nil && info.IsDir() {
						if err := watcher.Add(cleanCredlistsDir); err != nil {
							slog.Warn("failed to watch credlists directory", "dir", cleanCredlistsDir, "error", err)
						}
					}
				}
				if isReloadEvent(event, cleanConfigPath, cleanCredlistsDir) {
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.NewTimer(1 * time.Second)
					debounceCh = debounceTimer.C
				}
			case <-debounceCh:
				debounceCh = nil
				if err := conf.SetConfig(path); err != nil {
					slog.Error("failed to reload config", "error", err)
					continue
				}
				if onReload != nil {
					onReload()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Config watcher error", "error", err)
			}
		}
	}()

	return nil
}
