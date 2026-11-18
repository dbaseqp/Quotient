package checks

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebCheckSendsBrowserHeaders(t *testing.T) {
	var got []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Clone())
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	var port int
	_, err := fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)

	c := &Web{
		Service: Service{Target: "127.0.0.1", Port: port, Timeout: 5},
		Scheme:  "http",
		Url:     []urlData{{Path: "/start", Status: 200}},
	}
	resultsChan := make(chan Result, 1)
	c.Run(1, "01", 1, resultsChan)
	select {
	case result := <-resultsChan:
		require.True(t, result.Status, result.Error+": "+result.Debug)
	case <-time.After(10 * time.Second):
		t.Fatal("check timed out")
	}

	require.Len(t, got, 2, "expected initial request and one redirect hop")
	for _, h := range got {
		assert.Equal(t, chromeNavigationAccept, h.Get("Accept"))
		assert.Equal(t, chromeAcceptLanguage, h.Get("Accept-Language"))
		assert.Contains(t, h.Get("User-Agent"), " Chrome/")
		assert.Empty(t, h.Get("Referer"), "no Referer on a typed-in navigation or its redirect")
	}
	assert.Equal(t, got[0].Get("User-Agent"), got[1].Get("User-Agent"), "a redirect is followed by the same browser")
}
