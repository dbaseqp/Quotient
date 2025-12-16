package config

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// setupWatcher creates and configures the file system watcher
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

	return watcher, nil
}

// WatchConfig watches for changes to the config file
func (conf *ConfigSettings) WatchConfig(path string) error {
	watcher, err := setupWatcher(path)
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			if err := watcher.Close(); err != nil {
				slog.Error("failed to close watcher", "error", err)
			}
		}()

		var debounceTimer *time.Timer
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(1*time.Second, func() {
						if err := conf.SetConfig(path); err != nil {
							slog.Error("failed to reload config", "error", err)
						}
					})
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
