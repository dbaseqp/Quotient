package checks

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func headerNames(fields []headerField) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	return names
}

// Values below were captured from Chrome 151 (chromedp/headless-shell) with
// tellcap; headless-shell omits Accept-Language, which desktop Chrome sends
// last.
func TestChromeSecCHUAMatchesChrome(t *testing.T) {
	assert.Equal(t, `"Not=A?Brand";v="99", "Chrome";v="151", "Chromium";v="151"`, ChromeSecCHUA(151))
}

func TestNavigationHeadersFollowChromeOrder(t *testing.T) {
	p := browserProfiles[0]
	assert.Equal(t, []string{
		"Connection", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "Upgrade-Insecure-Requests",
		"User-Agent", "Accept", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-User", "Sec-Fetch-Dest",
		"Accept-Encoding", "Accept-Language",
	}, headerNames(p.navigationHeaders(true)))
	assert.Equal(t, []string{
		"Connection", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Accept-Encoding", "Accept-Language",
	}, headerNames(p.navigationHeaders(false)))

	for _, f := range p.navigationHeaders(false) {
		switch f.name {
		case "Accept":
			assert.Equal(t, "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7", f.value)
		case "Accept-Encoding":
			assert.Equal(t, "gzip, deflate", f.value)
		case "Connection":
			assert.Equal(t, "keep-alive", f.value)
		}
	}
}

func TestBrowserProfilesAreConsistent(t *testing.T) {
	platforms := map[string]string{"Windows NT": `"Windows"`, "Macintosh": `"macOS"`, "X11; Linux": `"Linux"`}
	for _, p := range browserProfiles {
		ua := p.userAgent()
		assert.Contains(t, ua, "Chrome/158.0.0.0 Safari/537.36", p.name)
		matched := false
		for marker, platform := range platforms {
			if strings.Contains(ua, marker) {
				assert.Equal(t, platform, p.platform, p.name)
				matched = true
			}
		}
		assert.True(t, matched, "no platform for %s", p.name)
		spec, err := p.helloSpec()
		require.NoError(t, err, p.name)
		var alpn []string
		var sigalgs []utls.SignatureScheme
		for _, ext := range spec.Extensions {
			switch e := ext.(type) {
			case *utls.ALPNExtension:
				alpn = e.AlpnProtocols
			case *utls.SignatureAlgorithmsExtension:
				sigalgs = e.SupportedSignatureAlgorithms
			}
		}
		assert.Equal(t, []string{"h2", "http/1.1"}, alpn, p.name)
		require.GreaterOrEqual(t, len(sigalgs), 3, p.name)
		assert.Equal(t, chromeMLDSASignatureSchemes, sigalgs[:3], p.name)
		for _, f := range p.navigationHeaders(true) {
			if f.name == "sec-ch-ua" {
				assert.Contains(t, f.value, `"Chrome";v="158"`)
			}
		}
	}
	seen := map[string]bool{}
	for range 300 {
		seen[randomBrowserProfile().name] = true
	}
	assert.Len(t, seen, len(browserProfiles))
}

func TestIsTrustworthyOrigin(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://10.100.101.112/":      true,
		"http://10.100.101.112:8080/":  false,
		"http://127.0.0.1:8080/":       true,
		"http://localhost/":            true,
		"http://[::1]/":                true,
		"http://gallery.team01.local/": false,
	} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		assert.Equal(t, want, isTrustworthyOrigin(u), raw)
	}
}

func TestHostHeaderDropsDefaultPort(t *testing.T) {
	for raw, want := range map[string]string{
		"http://10.0.0.5:80/":    "10.0.0.5",
		"https://10.0.0.5:443/":  "10.0.0.5",
		"http://10.0.0.5:8080/":  "10.0.0.5:8080",
		"https://10.0.0.5:8443/": "10.0.0.5:8443",
		"http://[fd00::1]:80/":   "[fd00::1]",
	} {
		req, err := http.NewRequest("GET", raw, nil)
		require.NoError(t, err)
		assert.Equal(t, want, hostHeader(req), raw)
	}
}

// TestBrowserTransportWireOrder reads the raw request head and checks that
// headers leave in the profile's order and spelling, with Host first and
// extra request headers after the profile's.
func TestBrowserTransportWireOrder(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	heads := make(chan []string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		r := bufio.NewReader(c)
		var lines []string
		for {
			l, err := r.ReadString('\n')
			if err != nil || l == "\r\n" {
				break
			}
			lines = append(lines, strings.TrimRight(l, "\r\n"))
		}
		heads <- lines
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	}()

	p := browserProfiles[0]
	tr := newBrowserTransport(p)
	defer tr.CloseIdleConnections()
	req, err := http.NewRequest("GET", "http://"+ln.Addr().String()+"/a?b=c", nil)
	require.NoError(t, err)
	req.Header.Set("X-Extra", "1")
	resp, err := (&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "ok", string(body))

	lines := <-heads
	want := []string{"GET /a?b=c HTTP/1.1", "Host: " + ln.Addr().String()}
	for _, f := range p.navigationHeaders(true) { // 127.0.0.1 is a trustworthy origin
		want = append(want, f.name+": "+f.value)
	}
	want = append(want, "X-Extra: 1")
	assert.Equal(t, want, lines)
}

// connCounter counts distinct client connections seen by a test server.
type connCounter struct {
	mu    sync.Mutex
	addrs map[string]bool
}

func (c *connCounter) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.addrs[r.RemoteAddr] = true
		c.mu.Unlock()
		h.ServeHTTP(w, r)
	})
}

// TestBrowserTransportProtocolsAndReuse checks HTTP/1.1 and HTTP/2 over the
// uTLS ClientHello, that a redirect reuses the keep-alive connection like a
// browser, and that Sec-Fetch-* and client hints arrive over HTTPS.
func TestBrowserTransportProtocolsAndReuse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tls   bool
		h2    bool
		proto int
	}{
		{"http", false, false, 1},
		{"https-h1", true, false, 1},
		{"https-h2", true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cc := &connCounter{addrs: map[string]bool{}}
			var mu sync.Mutex
			var got []*http.Request
			srv := httptest.NewUnstartedServer(cc.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = append(got, r)
				mu.Unlock()
				if r.URL.Path == "/start" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("final page"))
			})))
			srv.EnableHTTP2 = tc.h2
			scheme := "http"
			if tc.tls {
				srv.StartTLS()
				scheme = "https"
			} else {
				srv.Start()
			}
			defer srv.Close()

			tr := newBrowserTransport(browserProfiles[1])
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
			resp, err := client.Get(scheme + "://" + srv.Listener.Addr().String() + "/start")
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "final page", string(body))

			require.Len(t, got, 2)
			assert.Len(t, cc.addrs, 1, "the redirect should reuse the first connection")
			for _, r := range got {
				assert.Equal(t, tc.proto, r.ProtoMajor)
				assert.Equal(t, browserProfiles[1].userAgent(), r.UserAgent())
				assert.Equal(t, "navigate", r.Header.Get("Sec-Fetch-Mode"))
				assert.Equal(t, `"macOS"`, r.Header.Get("Sec-Ch-Ua-Platform"))
				if tc.tls {
					assert.Equal(t, []string{"gzip, deflate, br, zstd"}, r.Header.Values("Accept-Encoding"))
				}
				if tc.h2 {
					assert.Equal(t, "u=0, i", r.Header.Get("Priority"))
				}
			}
		})
	}
}

// TestBrowserTransportRetriesStaleIdleConnection: when the server closed the
// kept-alive connection, the next request goes out on a new one.
func TestBrowserTransportRetriesStaleIdleConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	tr := newBrowserTransport(browserProfiles[0])
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	for i := range 2 {
		resp, err := client.Get(srv.URL)
		require.NoError(t, err, "request %d", i)
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		srv.CloseClientConnections()
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDecodeResponse(t *testing.T) {
	const page = `<html><a href="/gif/wilee3.gif"></a></html>`
	encode := map[string]func(io.Writer) io.WriteCloser{
		"gzip":        func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) },
		"deflate":     func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) },
		"deflate-raw": func(w io.Writer) io.WriteCloser { fw, _ := flate.NewWriter(w, flate.DefaultCompression); return fw },
		"br":          func(w io.Writer) io.WriteCloser { return brotli.NewWriter(w) },
		"zstd":        func(w io.Writer) io.WriteCloser { zw, _ := zstd.NewWriter(w); return zw },
		"gzip, br":    nil,
		"identity":    nil,
		"compress":    nil, // unknown coding: left as is
	}
	for coding, enc := range encode {
		t.Run(coding, func(t *testing.T) {
			var buf bytes.Buffer
			header := strings.TrimSuffix(coding, "-raw")
			switch {
			case enc != nil:
				w := enc(&buf)
				_, _ = w.Write([]byte(page))
				require.NoError(t, w.Close())
			case coding == "gzip, br":
				var inner bytes.Buffer
				gw := gzip.NewWriter(&inner)
				_, _ = gw.Write([]byte(page))
				require.NoError(t, gw.Close())
				bw := brotli.NewWriter(&buf)
				_, _ = bw.Write(inner.Bytes())
				require.NoError(t, bw.Close())
			default:
				buf.WriteString(page)
			}
			resp := &http.Response{Header: http.Header{"Content-Encoding": {header}}, Body: io.NopCloser(&buf)}
			decodeResponse(resp)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, page, string(body))
			if coding == "compress" || coding == "identity" {
				assert.Equal(t, coding, resp.Header.Get("Content-Encoding"), "header left untouched")
			} else {
				assert.Empty(t, resp.Header.Get("Content-Encoding"))
			}
		})
	}

	t.Run("empty body with coding", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"Content-Encoding": {"gzip"}}, Body: http.NoBody}
		decodeResponse(resp)
		require.NoError(t, resp.Body.Close())
	})
}
