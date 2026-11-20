package checks

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	socksAtypIPv4   = 0x01
	socksAtypDomain = 0x03
)

// socksRequest is a destination requested through the test SOCKS5 server.
type socksRequest struct {
	atyp byte
	host string
	port int
}

// testSocks5Server is a minimal RFC 1928 server: no-auth, CONNECT only,
// IPv4 and DOMAINNAME address types. Every CONNECT is forwarded to backend.
type testSocks5Server struct {
	listener net.Listener
	backend  string

	mu       sync.Mutex
	requests []socksRequest
}

// startTestSocks5Server listens on a loopback port whose last digit becomes
// the returned team identifier, so "_" in a proxy URL can stand in for it.
// proxyPrefix is the port with its last digit removed.
func startTestSocks5Server(t *testing.T, backend string) (s *testSocks5Server, proxyPrefix, identifier string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s = &testSocks5Server{listener: l, backend: backend}
	t.Cleanup(func() { _ = l.Close() })
	go s.serve()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	return s, port[:len(port)-1], port[len(port)-1:]
}

func (s *testSocks5Server) serve() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *testSocks5Server) seen() []socksRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]socksRequest(nil), s.requests...)
}

func (s *testSocks5Server) handle(c net.Conn) {
	defer func() { _ = c.Close() }()

	// greeting: VER NMETHODS METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil { // no authentication
		return
	}

	// request: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 5 {
		return
	}
	if req[1] != 1 { // CONNECT only
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch req[3] {
	case socksAtypIPv4:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case socksAtypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		name := make([]byte, l[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return
		}
		host = string(name)
	default:
		_, _ = c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, socksRequest{atyp: req[3], host: host, port: int(pb[0])<<8 | int(pb[1])})
	s.mu.Unlock()

	up, err := net.Dial("tcp", s.backend)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = up.Close() }()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}

func runWebForTest(t *testing.T, w *Web, identifier string) Result {
	t.Helper()
	ch := make(chan Result, 1)
	w.Run(1, identifier, 1, ch)
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("check timed out")
		return Result{}
	}
}

func TestWebRun_Socks5Proxy(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		_, _ = w.Write([]byte("proxied ok"))
	}))
	defer backend.Close()
	_, backendPortStr, _ := net.SplitHostPort(backend.Listener.Addr().String())
	backendPort, err := strconv.Atoi(backendPortStr)
	require.NoError(t, err)

	t.Run("hostname reaches proxy unresolved", func(t *testing.T) {
		mu.Lock()
		hosts = nil
		mu.Unlock()
		socks, prefix, id := startTestSocks5Server(t, backend.Listener.Addr().String())

		w := &Web{
			Service: Service{Target: "canonical.example.test", Port: backendPort, Timeout: 5},
			Scheme:  "http",
			Url:     []urlData{{Path: "/", Status: 200, Regex: "proxied ok"}},
			Proxy:   "socks5h://127.0.0.1:" + prefix + id,
		}
		r := runWebForTest(t, w, "01")
		require.True(t, r.Status, "error=%q debug=%q", r.Error, r.Debug)

		reqs := socks.seen()
		require.Len(t, reqs, 1)
		assert.Equal(t, byte(socksAtypDomain), reqs[0].atyp, "destination must be sent as a DOMAINNAME")
		assert.Equal(t, "canonical.example.test", reqs[0].host)
		assert.Equal(t, backendPort, reqs[0].port)

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, []string{fmt.Sprintf("canonical.example.test:%d", backendPort)}, hosts)
	})

	t.Run("underscore in proxy is replaced by team identifier", func(t *testing.T) {
		socks, prefix, id := startTestSocks5Server(t, backend.Listener.Addr().String())

		w := &Web{
			Service: Service{Target: "canonical.example.test", Port: backendPort, Timeout: 5},
			Scheme:  "http",
			Url:     []urlData{{Path: "/", Status: 200}},
			Proxy:   "socks5h://127.0.0.1:" + prefix + "_",
		}
		r := runWebForTest(t, w, id)
		require.True(t, r.Status, "error=%q debug=%q", r.Error, r.Debug)
		reqs := socks.seen()
		require.Len(t, reqs, 1)
		assert.Equal(t, "canonical.example.test", reqs[0].host)
	})

	t.Run("refused proxy fails the check", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		require.NoError(t, l.Close()) // nothing listens here now

		w := &Web{
			Service: Service{Target: "canonical.example.test", Port: backendPort, Timeout: 2},
			Scheme:  "http",
			Url:     []urlData{{Path: "/", Status: 200}},
			Proxy:   fmt.Sprintf("socks5h://127.0.0.1:%d", port),
		}
		r := runWebForTest(t, w, "01")
		assert.False(t, r.Status)
		assert.NotEmpty(t, r.Error)
	})

	t.Run("proxy with a scheme other than socks5 fails the check", func(t *testing.T) {
		w := &Web{
			Service: Service{Target: "canonical.example.test", Port: backendPort, Timeout: 2},
			Scheme:  "http",
			Url:     []urlData{{Path: "/", Status: 200}},
			Proxy:   "http://127.0.0.1:1080",
		}
		r := runWebForTest(t, w, "01")
		assert.False(t, r.Status)
		assert.Equal(t, "invalid proxy URL", r.Error)
		assert.NotEmpty(t, r.Debug)
	})
}

func TestWebVerify_Proxy(t *testing.T) {
	tests := []struct {
		name    string
		proxy   string
		wantErr string
	}{
		{name: "empty accepted", proxy: ""},
		{name: "socks5h accepted", proxy: "socks5h://172.18.0.54:80_"},
		{name: "socks5 accepted", proxy: "socks5://proxy.example.test:1080"},
		{name: "port that is only the placeholder accepted", proxy: "socks5h://proxy.example.test:_"},
		{name: "http scheme rejected", proxy: "http://127.0.0.1:8080", wantErr: "socks5"},
		{name: "no scheme rejected", proxy: "127.0.0.1:1080", wantErr: "invalid proxy"},
		{name: "missing port rejected", proxy: "socks5h://127.0.0.1", wantErr: "no port"},
		{name: "missing host rejected", proxy: "socks5h://:1080", wantErr: "no host"},
		{name: "port out of range rejected", proxy: "socks5h://127.0.0.1:70000", wantErr: "outside 1-65535"},
		{name: "credentials rejected", proxy: "socks5h://user:secret@127.0.0.1:1080", wantErr: "credentials"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Web{
				Service: Service{Target: "10.100.1_.2"},
				Scheme:  "http",
				Url:     []urlData{{Path: "/", Status: 200}},
				Proxy:   tt.proxy,
			}
			err := w.Verify("web01", "10.100.1_.2", 1, 5, 0, 0)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), "web01-web", "error should name the check")
		})
	}
}
