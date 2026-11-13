package api

import (
	"fmt"
	"net/http"
	"time"
)

const (
	sseKeepaliveInterval = 25 * time.Second
	// sseStreamLifetime bounds a stream so the client reconnects through
	// authentication: a session or role revoked mid-stream stops receiving events.
	sseStreamLifetime = 10 * time.Minute
)

func (a *API) StreamEvents(w http.ResponseWriter, r *http.Request) {
	if !a.CheckCompetitionStarted(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Streaming unsupported"})
		return
	}

	ch, unsubscribe := a.eng.SubscribeRounds()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(w, ": ok\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()
	expired := time.NewTimer(sseStreamLifetime)
	defer expired.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-expired.C:
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "event: round\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
