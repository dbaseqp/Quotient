package checks

import (
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A team web server can stream an endless body; the check must stop after a
// bounded prefix instead of buffering everything the link delivers.
func TestWebReadsBoundedBodyPrefix(t *testing.T) {
	const served = 128 << 20
	var sent atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(served))
		chunk := make([]byte, 1<<20)
		for range served / len(chunk) {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c := &Web{Url: []urlData{{Path: "/"}}}
	require.NoError(t, c.Verify("box01", "127.0.0.1", 10, 10, 0, 0))
	c.Port = ln.Addr().(*net.TCPAddr).Port

	results := make(chan Result, 1)
	c.Run(1, "01", 1, results)
	select {
	case r := <-results:
		require.True(t, r.Status, "error=%q debug=%q", r.Error, r.Debug)
	case <-time.After(15 * time.Second):
		t.Fatal("no result")
	}
	require.LessOrEqual(t, sent.Load(), int64(32<<20),
		"the check pulled %d MiB of a team-controlled body into runner memory", sent.Load()>>20)
}
