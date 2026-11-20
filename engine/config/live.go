package config

import "sync/atomic"

// Live holds the configuration the engine and web server share. A reload
// publishes a freshly loaded snapshot instead of overwriting the current one,
// so a reader that takes one Current() sees a consistent config that never
// changes underneath it. Published snapshots must not be modified.
type Live struct {
	cur atomic.Pointer[ConfigSettings]
}

// NewLive returns a Live that publishes conf.
func NewLive(conf *ConfigSettings) *Live {
	l := &Live{}
	l.cur.Store(conf)
	return l
}

// Current returns the latest published snapshot.
func (l *Live) Current() *ConfigSettings {
	return l.cur.Load()
}

// Reload loads and validates the config at path and publishes it. On error the
// current snapshot stays in place.
func (l *Live) Reload(path string) error {
	next := &ConfigSettings{}
	if err := next.SetConfig(path); err != nil {
		return err
	}
	l.cur.Store(next)
	return nil
}
