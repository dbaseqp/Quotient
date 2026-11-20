package checks

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
		assert.Equal(t, "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", h.Get("Accept"))
		assert.NotContains(t, strings.ToLower(h.Get("Accept")), "json")
		assert.Contains(t, webAcceptLanguages, h.Get("Accept-Language"))
		assert.NotEmpty(t, h.Get("User-Agent"))
	}
	assert.Equal(t, got[0].Get("Accept-Language"), got[1].Get("Accept-Language"), "redirect must keep the same Accept-Language")
}

func TestRandomAcceptLanguage(t *testing.T) {
	assert.GreaterOrEqual(t, len(webAcceptLanguages), 3)
	for i := 0; i < 500; i++ {
		assert.Contains(t, webAcceptLanguages, randomAcceptLanguage())
	}
}
