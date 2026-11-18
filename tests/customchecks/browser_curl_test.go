package customchecks

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbaseqp/Quotient/engine/checks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawRequest is one request head as it came off the wire.
type rawRequest struct {
	Method, Path string
	Names        []string          // header names in wire order
	Headers      map[string]string // lower-case name -> value
	Body         string
}

// rawServer answers every request with "hello <method> <body>", gzip-encoded
// if the client accepts gzip (HTTP 500 for /fail), and records the request
// head in wire order, which net/http's server does not keep.
type rawServer struct {
	ln   net.Listener
	mu   sync.Mutex
	reqs []rawRequest
}

func startRawServer(t *testing.T, useTLS bool) *rawServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	if useTLS {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, NextProtos: []string{"http/1.1"}})
	}
	s := &rawServer{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *rawServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *rawServer) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return
	}
	req := rawRequest{Method: parts[0], Path: parts[1], Headers: map[string]string{}}
	for {
		h, err := br.ReadString('\n')
		if err != nil {
			return
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" {
			break
		}
		name, value, _ := strings.Cut(h, ":")
		req.Names = append(req.Names, name)
		req.Headers[strings.ToLower(name)] = strings.TrimSpace(value)
	}
	if n, _ := strconv.Atoi(req.Headers["content-length"]); n > 0 {
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		req.Body = string(body)
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()

	body := []byte(fmt.Sprintf("hello %s %s", req.Method, req.Body))
	encoding := ""
	if strings.Contains(req.Headers["accept-encoding"], "gzip") {
		var zipped bytes.Buffer
		zw := gzip.NewWriter(&zipped)
		_, _ = zw.Write(body)
		_ = zw.Close()
		body, encoding = zipped.Bytes(), "Content-Encoding: gzip\r\n"
	}
	status := "200 OK"
	if req.Path == "/fail" {
		status = "500 Internal Server Error"
	}
	_, _ = fmt.Fprintf(c, "HTTP/1.1 %s\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n", status, encoding, len(body))
	_, _ = c.Write(body)
}

func (s *rawServer) take(t *testing.T) []rawRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reqs
	s.reqs = nil
	return out
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "browser-curl test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// sh runs script with browser-curl.sh sourced and returns stdout and the exit code.
func sh(t *testing.T, script string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "custom-checks", "browser-curl.sh"))
	require.NoError(t, err)
	cmd := exec.Command("sh", "-c", ". "+helper+"\n"+script)
	cmd.Env = append(os.Environ(), checks.ChromeEnv()...) // as the runner runs custom checks
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return out.String(), code
}

var chromeUA = regexp.MustCompile(`^Mozilla/5\.0 \((Windows NT 10\.0; Win64; x64|Macintosh; Intel Mac OS X 10_15_7|X11; Linux x86_64)\) AppleWebKit/537\.36 \(KHTML, like Gecko\) Chrome/` + strconv.Itoa(checks.ChromeMajor) + `\.0\.0\.0 Safari/537\.36$`)

// Chrome 151's navigation headers (captured with chromedp/headless-shell),
// plus Accept-Language, which desktop Chrome sends last.
func TestBrowserCurlPlainHTTPMatchesChrome(t *testing.T) {
	s := startRawServer(t, false)
	out, code := sh(t, fmt.Sprintf("browser_curl -s --resolve team.example:%d:127.0.0.1 http://team.example:%d/", s.port(), s.port()))
	require.Equal(t, 0, code)
	assert.Equal(t, "hello GET ", out, "body is decompressed")
	reqs := s.take(t)
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, []string{"Host", "Connection", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Accept-Encoding", "Accept-Language"}, r.Names)
	assert.Regexp(t, chromeUA, r.Headers["user-agent"])
	assert.Equal(t, "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7", r.Headers["accept"])
	assert.Equal(t, "gzip, deflate", r.Headers["accept-encoding"])
	assert.Equal(t, fmt.Sprintf("team.example:%d", s.port()), r.Headers["host"])
}

func TestBrowserCurlHTTPSMatchesChrome(t *testing.T) {
	s := startRawServer(t, true)
	out, code := sh(t, fmt.Sprintf("browser_curl -s -k https://127.0.0.1:%d/", s.port()))
	require.Equal(t, 0, code)
	assert.Equal(t, "hello GET ", out)
	r := s.take(t)[0]
	assert.Equal(t, []string{"Host", "Connection", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-User", "Sec-Fetch-Dest", "Accept-Encoding", "Accept-Language"}, r.Names)
	assert.Equal(t, checks.ChromeSecCHUA(checks.ChromeMajor), r.Headers["sec-ch-ua"])
	platform := map[string]string{"Windows NT": `"Windows"`, "Macintosh": `"macOS"`, "X11": `"Linux"`}
	for os, want := range platform {
		if strings.Contains(r.Headers["user-agent"], os) {
			assert.Equal(t, want, r.Headers["sec-ch-ua-platform"])
		}
	}
	features, _ := exec.Command("curl", "--version").Output()
	want := "gzip, deflate"
	if bytes.Contains(features, []byte("brotli")) && bytes.Contains(features, []byte("zstd")) {
		want = "gzip, deflate, br, zstd"
	}
	assert.Equal(t, want, r.Headers["accept-encoding"])
}

// The method, path, body, credentials, output and exit code are what plain
// curl produces with the same arguments. Plain curl gets the body
// uncompressed; browser_curl gets it gzipped and decompresses it.
func TestBrowserCurlKeepsRequestContentAndResult(t *testing.T) {
	s := startRawServer(t, false)
	cases := []string{
		"-s -u 'user:p@ss' http://127.0.0.1:%d/api/health",
		"-s -X PUT -d 'a=b&c=d' http://127.0.0.1:%d/api/item",
		"-s -H 'Content-Type: application/json' --data-binary '{\"k\":1}' http://127.0.0.1:%d/api/json",
		"-s -f http://127.0.0.1:%d/fail",
		"-s -o /dev/null -w '%%{http_code}' http://127.0.0.1:%d/fail",
	}
	for _, c := range cases {
		args := fmt.Sprintf(c, s.port())
		plainOut, plainCode := sh(t, "curl "+args)
		plain := s.take(t)
		browserOut, browserCode := sh(t, "browser_curl "+args)
		browser := s.take(t)
		require.Len(t, plain, 1, args)
		require.Len(t, browser, 1, args)
		assert.Equal(t, plainCode, browserCode, args)
		assert.Equal(t, plainOut, browserOut, args)
		assert.Contains(t, browser[0].Headers["accept-encoding"], "gzip", args)
		p, b := plain[0], browser[0]
		assert.Equal(t, p.Method, b.Method, args)
		assert.Equal(t, p.Path, b.Path, args)
		assert.Equal(t, p.Body, b.Body, args)
		for _, h := range []string{"host", "authorization", "content-type", "content-length"} {
			assert.Equal(t, p.Headers[h], b.Headers[h], "%s: %s", args, h)
		}
	}
}

func TestBrowserCurlCheckHeadersWin(t *testing.T) {
	s := startRawServer(t, false)
	_, code := sh(t, fmt.Sprintf("browser_curl -s -A 'probe/1' -H 'Accept: application/json' -H 'Host: wilee.acme.anvil' -H 'Cookie: a=b' http://127.0.0.1:%d/", s.port()))
	require.Equal(t, 0, code)
	r := s.take(t)[0]
	assert.Equal(t, "probe/1", r.Headers["user-agent"])
	assert.Equal(t, "application/json", r.Headers["accept"])
	assert.Equal(t, "wilee.acme.anvil", r.Headers["host"])
	count := map[string]int{}
	for _, n := range r.Names {
		count[strings.ToLower(n)]++
	}
	assert.Equal(t, 1, count["accept"])
	assert.Equal(t, 1, count["user-agent"])
	assert.Equal(t, "Host", r.Names[0])
	assert.Equal(t, "Cookie", r.Names[len(r.Names)-1])
}

// A shell check run by the runner claims checks.ChromeMajor, the one place the
// version is kept; outside the runner browser-curl.sh refuses to guess one.
func TestBrowserCurlClaimsChromeMajorInCustomCheck(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "custom-checks", "browser-curl.sh"))
	require.NoError(t, err)
	s := startRawServer(t, false)
	c := &checks.Custom{
		Service: checks.Service{Name: "test-browser-curl", Target: "127.0.0.1", Timeout: 10},
		Command: fmt.Sprintf(". %s; browser_curl -s http://127.0.0.1:%d/", helper, s.port()),
		Regex:   "hello GET",
	}
	results := make(chan checks.Result, 1)
	c.Run(1, "01", 1, results)
	res := <-results
	require.True(t, res.Status, res.Error+" "+res.Debug)
	reqs := s.take(t)
	require.Len(t, reqs, 1)
	assert.Contains(t, reqs[0].Headers["user-agent"], " Chrome/"+strconv.Itoa(checks.ChromeMajor)+".0.0.0 ")

	cmd := exec.Command("sh", "-c", ". "+helper)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "sourcing without the runner's Chrome version must fail")
	assert.Contains(t, string(out), "QUOTIENT_CHROME_MAJOR")
}
