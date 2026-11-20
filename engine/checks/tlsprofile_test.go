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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// The header order below was captured from Chrome 151 (chromedp/headless-shell)
// with tellcap; headless-shell omits Accept-Language, which desktop Chrome
// sends last.
func TestNavigationHeadersFollowChromeOrder(t *testing.T) {
	assert.Equal(t, []string{
		"Connection", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "Upgrade-Insecure-Requests",
		"User-Agent", "Accept", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-User", "Sec-Fetch-Dest",
		"Accept-Encoding", "Accept-Language",
	}, headerNames(navigationHeaders(clientWindows, true)))
	assert.Equal(t, []string{
		"Connection", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Accept-Encoding", "Accept-Language",
	}, headerNames(navigationHeaders(clientWindows, false)))

	for _, f := range navigationHeaders(clientWindows, false) {
		switch f.name {
		case "Accept":
			assert.Equal(t, "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7", f.value)
		case "Accept-Encoding":
			assert.Equal(t, "gzip, deflate", f.value)
		case "Connection":
			assert.Equal(t, "keep-alive", f.value)
		}
	}
	for _, f := range navigationHeaders(clientWindows, true) {
		switch f.name {
		case "User-Agent":
			assert.Equal(t, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/158.0.0.0 Safari/537.36", f.value)
		case "sec-ch-ua":
			assert.Equal(t, `"Chromium";v="158", "Not.A/Brand";v="24", "Google Chrome";v="158"`, f.value)
		case "sec-ch-ua-platform":
			assert.Equal(t, `"Windows"`, f.value)
		}
	}
}

func TestChromeHelloSpecOffersH2AndMLDSA(t *testing.T) {
	spec, err := chromeHelloSpec()
	require.NoError(t, err)
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
	assert.Equal(t, []string{"h2", "http/1.1"}, alpn)
	require.GreaterOrEqual(t, len(sigalgs), 3)
	assert.Equal(t, chromeMLDSASignatureSchemes, sigalgs[:3])
}

func TestRandomClientOSPicksEveryOS(t *testing.T) {
	seen := map[clientOS]bool{}
	for range 300 {
		seen[randomClientOS()] = true
	}
	assert.Len(t, seen, len(chromeClientOSes))
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
// exactly Chrome's headers leave, in Chrome's order and spelling, with Host
// first and the request's own headers left out.
func TestBrowserTransportWireOrder(t *testing.T) {
	heads := make(chan []string, 1)
	addr, _ := serveRaw(t, func(c net.Conn) {
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
	})

	tr := newBrowserTransport(clientWindows)
	defer tr.CloseIdleConnections()
	req, err := http.NewRequest("GET", "http://"+addr+"/a?b=c", nil)
	require.NoError(t, err)
	req.Header.Set("X-Extra", "1")
	req.Header.Set("Referer", "http://example.com/")
	resp, err := (&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "ok", string(body))

	lines := <-heads
	want := []string{"GET /a?b=c HTTP/1.1", "Host: " + addr}
	for _, f := range navigationHeaders(clientWindows, true) { // 127.0.0.1 is a trustworthy origin
		want = append(want, f.name+": "+f.value)
	}
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

			tr := newBrowserTransport(clientMacOS)
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
				uaOS, _ := clientMacOS.chromeIdentity()
				assert.Equal(t, ChromeUserAgent(uaOS), r.UserAgent())
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
	tr := newBrowserTransport(clientWindows)
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

// serveRaw accepts connections on a new listener and hands each to handle.
// It returns the listener's address and the number of connections accepted.
func serveRaw(t *testing.T, handle func(net.Conn)) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer func() { _ = c.Close() }()
				handle(c)
			}()
		}
	}()
	return ln.Addr().String(), &accepted
}

// TestBrowserTransportDoesNotRetryMalformedResponse: a reused connection that
// answers with garbage fails the request; only a connection that ended before
// the response is retried on a new one.
func TestBrowserTransportDoesNotRetryMalformedResponse(t *testing.T) {
	addr, accepted := serveRaw(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		for i := 0; ; i++ {
			if _, err := http.ReadRequest(r); err != nil {
				return
			}
			reply := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
			if i > 0 {
				reply = "not a status line\r\n\r\n"
			}
			if _, err := io.WriteString(c, reply); err != nil {
				return
			}
		}
	})
	tr := newBrowserTransport(clientLinux)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())

	_, err = client.Get("http://" + addr + "/")
	assert.ErrorContains(t, err, "malformed HTTP")
	assert.EqualValues(t, 1, accepted.Load(), "the malformed response must not be retried on a new connection")
}

// TestBrowserTransportBoundsResponseHeaders: a response head larger than
// maxResponseHeaderBytes fails instead of being buffered.
func TestBrowserTransportBoundsResponseHeaders(t *testing.T) {
	addr, _ := serveRaw(t, func(c net.Conn) {
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		line := "X-Pad: " + strings.Repeat("a", 1000) + "\r\n"
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n"+strings.Repeat(line, 2*maxResponseHeaderBytes/len(line)))
	})
	tr := newBrowserTransport(clientLinux)
	defer tr.CloseIdleConnections()
	_, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("http://" + addr + "/")
	assert.ErrorIs(t, err, errResponseHeaderTooLarge)
}

// TestBrowserTransportBoundsH2ResponseHeaders: the same limit holds when the
// server selects HTTP/2.
func TestBrowserTransportBoundsH2ResponseHeaders(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pad := strings.Repeat("a", 1000)
		for i := range 2 * maxResponseHeaderBytes / len(pad) {
			w.Header().Set("X-Pad-"+strconv.Itoa(i), pad)
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	tr := newBrowserTransport(clientLinux)
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + srv.Listener.Addr().String() + "/")
	if err == nil {
		_ = resp.Body.Close()
		assert.Equal(t, 2, resp.ProtoMajor)
	}
	assert.Error(t, err)
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

	for _, ce := range []string{"gzip", "deflate", "br", "zstd", "gzip, br, zstd", "br, br"} {
		t.Run("empty body with "+ce, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Content-Encoding": {ce}}, Body: http.NoBody}
			decodeResponse(resp)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Empty(t, body)
			require.NoError(t, resp.Body.Close())
		})
	}

	for _, ce := range []string{"gzip, br, zstd", "br, br", "gzip, x-gzip"} {
		t.Run("rejects "+ce, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Content-Encoding": {ce}}, Body: io.NopCloser(strings.NewReader("x"))}
			decodeResponse(resp)
			_, err := io.ReadAll(resp.Body)
			assert.Error(t, err)
		})
	}

	t.Run("unknown coding after a duplicate is left in place", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"Content-Encoding": {"gzip, gzip, compress"}}, Body: io.NopCloser(strings.NewReader(page))}
		decodeResponse(resp)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, page, string(body))
		assert.Equal(t, "gzip, gzip, compress", resp.Header.Get("Content-Encoding"))
	})
}
