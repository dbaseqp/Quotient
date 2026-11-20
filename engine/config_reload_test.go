package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An event.conf hot reload must not race the round loop: a reload publishes a
// new snapshot instead of overwriting the one a round is reading. Only -race
// detects the overwrite; one reload per round is too few for the detector, so
// this reloads continuously while rounds run.
func TestConfigReload_DoesNotRaceRoundLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	redis, pg := testutil.StartContainers(t)
	redis.Client.FlushDB(context.Background())
	require.NoError(t, pg.DB.ResetScores())
	pg.CreateTestTeam(t, "Team Reload", "01")

	se := NewTestEngine(t, redis, pg, 1)
	se.Config.Current().MiscSettings.Delay = 3
	svc := &mockRunner{Service: checks.Service{Display: "web", Name: "box01-web", ServiceType: "Mock", Points: 10}}
	se.Config.Current().Box = []config.Box{{Name: "box01", IP: "192.0.2.1", Runners: []checks.Runner{svc}}}
	fakeRunner(t, redis, nil)

	path := filepath.Join(t.TempDir(), "event.conf")
	require.NoError(t, os.WriteFile(path, []byte(`[RequiredSettings]
EventName = "Reloaded"
EventType = "rvb"
DBConnectURL = "postgres://x:x@localhost:5432/x"
BindAddress = "127.0.0.1"

[MiscSettings]
Delay = 3
Jitter = 1
`), 0o600))

	stop := make(chan struct{})
	reloads := make(chan int, 1)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				reloads <- n
				return
			default:
			}
			if err := se.Config.Reload(path); err != nil { // what the config watcher calls
				t.Errorf("reload: %v", err)
			}
			n++
		}
	}()
	rounds := 0
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); rounds++ {
		require.NoError(t, se.rvb())
	}
	close(stop)
	t.Logf("rounds=%d reloads=%d", rounds, <-reloads)
	assert.Equal(t, "Reloaded", se.Config.Current().RequiredSettings.EventName)
}
