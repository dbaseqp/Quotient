package checks

import (
	"compress/gzip"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// galleryHandler is a small site that exercises every outcome of the web
// check: status codes, regex hits and misses, redirects, a redirect loop,
// gzip bodies and a slow page.
func galleryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		_, _ = w.Write([]byte(`<html><a href="/gif/wilee7.gif">wile</a></html>`))
	case "/plain":
		_, _ = w.Write([]byte("no images here"))
	case "/gzip":
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`<html><a href="/gif/wilee42.gif">wile</a></html>`))
		_ = gz.Close()
	case "/redirect":
		http.Redirect(w, r, "/", http.StatusFound)
	case "/redirect-chain":
		http.Redirect(w, r, "/redirect", http.StatusMovedPermanently)
	case "/loop":
		http.Redirect(w, r, "/loop", http.StatusFound)
	case "/error":
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<a href="/gif/wilee1.gif">`))
	case "/slow":
		time.Sleep(3 * time.Second)
		_, _ = w.Write([]byte("late"))
	default:
		http.NotFound(w, r)
	}
}

// TestWebCheckScoringOutcomes pins the pass/fail result and error of the web
// check for each case, over HTTP, HTTPS with HTTP/1.1 and HTTPS with HTTP/2.
// The same expectations hold for the check before the browser-profile change
// (Go's default client), so the change does not alter scoring.
func TestWebCheckScoringOutcomes(t *testing.T) {
	const gallery = `href="/gif/wilee\d+\.gif"`
	cases := []struct {
		name    string
		url     urlData
		timeout int
		status  bool
		err     string
	}{
		{"regex match", urlData{Path: "/", Status: 200, Regex: gallery}, 5, true, ""},
		{"status only", urlData{Path: "/", Status: 200}, 5, true, ""},
		{"no status no regex", urlData{Path: "/error"}, 5, true, ""},
		{"regex miss", urlData{Path: "/plain", Status: 200, Regex: gallery}, 5, false, "didn't find regex on page"},
		{"wrong status", urlData{Path: "/missing", Status: 200, Regex: gallery}, 5, false, "status returned by webserver was incorrect"},
		{"500 not 200", urlData{Path: "/error", Status: 200}, 5, false, "status returned by webserver was incorrect"},
		{"regex on 500 without status", urlData{Path: "/error", Regex: gallery}, 5, true, ""},
		{"bad regex", urlData{Path: "/", Status: 200, Regex: `(`}, 5, false, "error compiling regex to match for web page"},
		{"redirect followed", urlData{Path: "/redirect", Status: 200, Regex: gallery}, 5, true, ""},
		{"redirect chain followed", urlData{Path: "/redirect-chain", Status: 200, Regex: gallery}, 5, true, ""},
		{"redirect loop", urlData{Path: "/loop", Status: 200}, 5, false, "web request errored out"},
		{"gzip body decoded", urlData{Path: "/gzip", Status: 200, Regex: `wilee42`}, 5, true, ""},
		{"slow page", urlData{Path: "/slow", Status: 200}, 1, false, "web request errored out"},
	}

	servers := map[string]*httptest.Server{
		"http":     httptest.NewServer(http.HandlerFunc(galleryHandler)),
		"https-h1": httptest.NewTLSServer(http.HandlerFunc(galleryHandler)),
	}
	h2 := httptest.NewUnstartedServer(http.HandlerFunc(galleryHandler))
	h2.EnableHTTP2 = true
	h2.StartTLS()
	servers["https-h2"] = h2
	for _, s := range servers {
		defer s.Close()
	}

	for kind, srv := range servers {
		scheme := "http"
		if kind != "http" {
			scheme = "https"
		}
		_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
		require.NoError(t, err)
		port, err := strconv.Atoi(portStr)
		require.NoError(t, err)

		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				c := Web{
					Service: Service{Name: "gallery", Target: "127.0.0.1", Port: port, Timeout: tc.timeout},
					Scheme:  scheme,
					Url:     []urlData{tc.url},
				}
				results := make(chan Result, 1)
				c.Run(1, "01", 1, results)
				select {
				case r := <-results:
					assert.Equal(t, tc.status, r.Status, "debug: %s", r.Debug)
					if tc.err == "web request errored out" && tc.timeout == 1 {
						// The HTTP client timeout and the service wrapper race, as in TestServiceTimeout.
						assert.Contains(t, []string{"web request errored out", "check timeout exceeded"}, r.Error)
					} else {
						assert.Equal(t, tc.err, r.Error, "debug: %s", r.Debug)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("check did not report")
				}
			})
		}
	}
}

// TestWebCheckConnectionRefused keeps the error for a closed port.
func TestWebCheckConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	for _, scheme := range []string{"http", "https"} {
		c := Web{Service: Service{Target: "127.0.0.1", Port: port, Timeout: 5}, Scheme: scheme, Url: []urlData{{Path: "/", Status: 200}}}
		results := make(chan Result, 1)
		c.Run(1, "01", 1, results)
		r := <-results
		assert.False(t, r.Status)
		assert.Equal(t, "web request errored out", r.Error)
		assert.True(t, strings.Contains(r.Debug, "connection refused"), r.Debug)
	}
}
