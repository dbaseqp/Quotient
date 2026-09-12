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

// A competition reset re-enters Start, which re-seeds the credential locks
// while the web server keeps serving. A team appearing in that re-seed writes
// the map under a concurrent reader, which Go treats as a fatal throw rather
// than a stale read. Run under -race.
func TestCredentialLocksConcurrentReseed(t *testing.T) {
	se := &ScoringEngine{credentialsMutex: map[uint]*sync.Mutex{}}
	se.setTeamCredentialLocks([]db.TeamSchema{{ID: 1}})

	stop := make(chan struct{})
	done := make(chan struct{})

	go func() { // the reset loop, seeing a team it has not seen before
		defer close(done)
		for id := uint(2); ; id++ {
			select {
			case <-stop:
				return
			default:
				se.setTeamCredentialLocks([]db.TeamSchema{{ID: id}})
			}
		}
	}()

	for i := 0; i < 5000; i++ { // a PCR request
		if _, err := se.teamcredentialsMutex(1); err != nil {
			break
		}
	}

	close(stop)
	<-done
}
