package engine

import (
	"errors"
	"sync"
	"testing"

	"quotient/engine/config"
)

// A team ID with no entry in CredentialsMutex must report an error rather than
// dereference a nil mutex.
func TestCredentialsUnknownTeam(t *testing.T) {
	se := &ScoringEngine{
		Config: &config.ConfigSettings{
			CredlistSettings: config.CredlistConfig{
				Credlist: []config.Credlist{{CredlistPath: "linux.credlist"}},
			},
		},
		CredentialsMutex: map[uint]*sync.Mutex{1: {}},
	}

	t.Run("update", func(t *testing.T) {
		_, _, err := se.UpdateCredentials(99, "linux.credlist", []string{"u"}, []string{"p"})
		if !errors.Is(err, errNoCredentialLock) {
			t.Fatalf("want errNoCredentialLock, got %v", err)
		}
	})

	t.Run("reset", func(t *testing.T) {
		err := se.ResetCredentials(99, "linux.credlist", "admin")
		if !errors.Is(err, errNoCredentialLock) {
			t.Fatalf("want errNoCredentialLock, got %v", err)
		}
	})
}
