package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/dbaseqp/Quotient/tests/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForRetryWaitsDelay(t *testing.T) {
	start := time.Now()
	assert.True(t, waitForRetry(context.Background(), 50*time.Millisecond, start.Add(time.Second)))
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
}

func TestWaitForRetrySkipsWhenDeadlineTooClose(t *testing.T) {
	start := time.Now()
	assert.False(t, waitForRetry(context.Background(), time.Second, start.Add(500*time.Millisecond)))
	assert.Less(t, time.Since(start), 100*time.Millisecond, "should not sleep when no retry fits")
}

func TestWaitForRetryStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, waitForRetry(ctx, time.Second, time.Now().Add(time.Minute)))
}

// TestMain lets the test binary stand in for the runner binary, which handleTask re-executes as "runner check" to
// run each check attempt.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "check" {
		main()
	}
	os.Exit(m.Run())
}

// serveTeam stands in for a team's service: it accepts connections on a loopback port, hands each to handle and
// counts them.
func serveTeam(t *testing.T, handle func(net.Conn)) (int, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	conns := &atomic.Int32{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				handle(c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, conns
}

// checkTask builds the task the engine would enqueue for check.
func checkTask(t *testing.T, check checks.Runner, attempts int, deadline time.Time) *engine.Task {
	t.Helper()
	data, err := json.Marshal(check)
	require.NoError(t, err)
	return &engine.Task{
		TeamID: 1, TeamIdentifier: "01", ServiceType: check.GetType(), ServiceName: check.GetName(), RoundID: 1,
		Attempts: attempts, Deadline: deadline, CheckData: data,
		Credentials: []engine.Credential{{Username: "u", Password: "p", Credlist: "l"}},
	}
}

// popResult returns the result handleTask pushed to Redis.
func popResult(t *testing.T, rdb *testutil.RedisContainer) checks.Result {
	t.Helper()
	raw, err := rdb.Client.LPop(context.Background(), "results").Result()
	require.NoError(t, err)
	var result checks.Result
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	return result
}

func TestHandleTaskRunsCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()
	require.NoError(t, rdb.Client.Del(ctx, "results").Err())

	port, _ := serveTeam(t, func(net.Conn) {})
	check := &checks.Tcp{Service: checks.Service{Port: port}}
	require.NoError(t, check.Verify("box01", "127.0.0.1", 5, 10, 0, 0))
	handleTask(ctx, rdb.Client, check, checkTask(t, check, 1, time.Now().Add(10*time.Second)))

	result := popResult(t, rdb)
	assert.True(t, result.Status, "error=%q debug=%q", result.Error, result.Debug)
	assert.Equal(t, "127.0.0.1", result.Target)
	assert.Equal(t, 5, result.Points)
	assert.Equal(t, "box01-tcp", result.ServiceName)
}

func TestHandleTaskKeepsPointsWhenRoundEndsFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()
	require.NoError(t, rdb.Client.Del(ctx, "results").Err())

	// An SMB server that never answers the NEGOTIATE keeps the check running past the round deadline.
	port, _ := serveTeam(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	check := &checks.Smb{Service: checks.Service{Port: port}}
	require.NoError(t, check.Verify("box01", "127.0.0.1", 7, 30, 0, 0))
	handleTask(ctx, rdb.Client, check, checkTask(t, check, 1, time.Now().Add(300*time.Millisecond)))

	result := popResult(t, rdb)
	assert.False(t, result.Status)
	assert.Equal(t, "timeout", result.Error)
	assert.Equal(t, 7, result.Points, "a failed check still counts toward the service's possible points")
}

// A team's server can crash a check library inside a goroutine the library starts itself, where no recover in
// Quotient can catch the panic. That must fail only the team's check, not kill the runner with every task on it.
func TestHandleTaskSurvivesCrashingCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rdb, _ := testutil.StartContainers(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		handle func(net.Conn)
		check  func(port int) checks.Runner
	}{
		{
			// hirochachacha/go-smb2 v1.1.0 slices a received frame as an SMB2 header in its receiver goroutine.
			name: "Smb",
			handle: func(c net.Conn) {
				buf := make([]byte, 4096)
				if _, err := c.Read(buf); err != nil {
					return
				}
				_, _ = c.Write([]byte{0, 0, 0, 1, 0xfe}) // a 1-byte frame in reply to NEGOTIATE
				_, _ = c.Read(buf)
			},
			check: func(port int) checks.Runner { return &checks.Smb{Service: checks.Service{Port: port}} },
		},
		{
			// emersion/go-imap v1.2.1 indexes the sequence number of an untagged EXPUNGE in its reader goroutine.
			name: "Imap",
			handle: func(c net.Conn) {
				r := bufio.NewReader(c)
				_, _ = fmt.Fprint(c, "* OK IMAP4rev1 ready\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					f := strings.Fields(line)
					switch {
					case len(f) < 2:
					case strings.EqualFold(f[1], "LOGIN"):
						_, _ = fmt.Fprintf(c, "* EXPUNGE\r\n%s OK LOGIN completed\r\n", f[0])
					default:
						_, _ = fmt.Fprintf(c, "%s OK done\r\n", f[0])
					}
				}
			},
			check: func(port int) checks.Runner {
				return &checks.Imap{Service: checks.Service{Port: port, CredLists: []string{"l"}}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, rdb.Client.Del(ctx, "results").Err())
			port, conns := serveTeam(t, tt.handle)
			check := tt.check(port)
			require.NoError(t, check.Verify("box01", "127.0.0.1", 10, 5, 0, 0))
			handleTask(ctx, rdb.Client, check, checkTask(t, check, 2, time.Now().Add(20*time.Second)))

			result := popResult(t, rdb)
			assert.False(t, result.Status)
			assert.Equal(t, "check crashed", result.Error)
			assert.Contains(t, result.Debug, "panic: ")
			assert.Equal(t, 10, result.Points)
			assert.Equal(t, uint(1), result.TeamID)
			assert.Equal(t, "box01-"+strings.ToLower(tt.name), result.ServiceName)
			assert.Equal(t, int32(2), conns.Load(), "a crashed attempt is still retried")
		})
	}
}
