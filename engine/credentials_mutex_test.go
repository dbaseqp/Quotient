package engine

import (
	"errors"
	"sync"
	"testing"

	"quotient/engine/config"
	"quotient/engine/db"
)

// A team ID with no entry in credentialsMutex must report an error rather than
// dereference a nil mutex.
func TestCredentialsUnknownTeam(t *testing.T) {
	se := &ScoringEngine{
		Config: &config.ConfigSettings{
			CredlistSettings: config.CredlistConfig{
				Credlist: []config.Credlist{{CredlistPath: "linux.credlist"}},
			},
		},
		credentialsMutex: map[uint]*sync.Mutex{1: {}},
	}

	if _, _, err := se.UpdateCredentials(99, "linux.credlist", []string{"u"}, []string{"p"}); !errors.Is(err, errNoCredentialLock) {
		t.Errorf("UpdateCredentials: want errNoCredentialLock, got %v", err)
	}
	if err := se.ResetCredentials(99, "linux.credlist", "admin"); !errors.Is(err, errNoCredentialLock) {
		t.Errorf("ResetCredentials: want errNoCredentialLock, got %v", err)
	}
}

// Seeding runs on the engine goroutine while the web server serves. A team
// appearing in a seed writes the map under a concurrent reader, which Go treats
// as a fatal throw rather than a stale read. Run under -race.
func TestCredentialLocksConcurrentReseed(t *testing.T) {
	se := &ScoringEngine{credentialsMutex: map[uint]*sync.Mutex{}}
	se.setTeamCredentialLocks([]db.TeamSchema{{ID: 1}})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // seeding, seeing teams it has not seen before
		defer wg.Done()
		for id := uint(2); id < 5002; id++ {
			se.setTeamCredentialLocks([]db.TeamSchema{{ID: id}})
		}
	}()

	for i := 0; i < 5000; i++ { // a PCR request
		if _, err := se.teamCredentialLock(1); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
