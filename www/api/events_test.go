package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamEvents_FlushesInitialHandshakeAndExitsOnCancel(t *testing.T) {
	a := &API{eng: &engine.ScoringEngine{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithIdentity(r.Context(), Identity{Username: "admin", Roles: []string{"admin"}})
		a.StreamEvents(w, r.WithContext(ctx))
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, ": ok\n", line)
	sep, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "\n", sep)

	cancel()
}

func TestStreamEvents_EndsAtStreamLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &API{eng: &engine.ScoringEngine{}}
		ctx := WithIdentity(t.Context(), Identity{Username: "admin", Roles: []string{"admin"}})
		// Without the lifetime bound the handler would run until this deadline.
		ctx, cancel := context.WithTimeout(ctx, 2*sseStreamLifetime)
		defer cancel()
		rec := httptest.NewRecorder()

		start := time.Now()
		a.StreamEvents(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx))

		assert.Equal(t, sseStreamLifetime, time.Since(start))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, strings.HasPrefix(rec.Body.String(), ": ok\n\n"), "got %q", rec.Body.String())
	})
}
