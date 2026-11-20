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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	defer func() { _ = c.Close() }()
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

// helperPath is the repo's browser-curl.sh, the file the runner image copies
// to /app/lib.
func helperPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "custom-checks", "browser-curl.sh"))
	require.NoError(t, err)
	return helper
}

// chromeEnv is checks.ChromeEnv, by name.
func chromeEnv(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, kv := range checks.ChromeEnv() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

// shRun runs script with browser-curl.sh sourced and env added to the
// environment, as the runner runs custom checks, and returns stdout, stderr
// and the exit code.
func shRun(t *testing.T, env map[string]string, script string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", ". "+helperPath(t)+"\n"+script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return out.String(), errOut.String(), code
}

// shEnv is shRun without stderr.
func shEnv(t *testing.T, env map[string]string, script string) (string, int) {
	t.Helper()
	out, _, code := shRun(t, env, script)
	return out, code
}

// sh is shEnv with checks.ChromeEnv.
func sh(t *testing.T, script string) (string, int) {
	t.Helper()
	return shEnv(t, chromeEnv(t), script)
}

const chromeAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"

// Chrome 151's navigation headers (captured with chromedp/headless-shell),
// plus Accept-Language, which desktop Chrome sends last.
func TestBrowserCurlPlainHTTPMatchesChrome(t *testing.T) {
	s := startRawServer(t, false)
	env := chromeEnv(t)
	out, code := shEnv(t, env, fmt.Sprintf("browser_curl -s --resolve team.example:%d:127.0.0.1 http://team.example:%d/", s.port(), s.port()))
	require.Equal(t, 0, code)
	assert.Equal(t, "hello GET ", out, "body is decompressed")
	reqs := s.take(t)
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, []string{"Host", "Connection", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Accept-Encoding", "Accept-Language"}, r.Names)
	assert.Equal(t, env["QUOTIENT_CHROME_USER_AGENT"], r.Headers["user-agent"])
	assert.Equal(t, chromeAccept, r.Headers["accept"])
	assert.Equal(t, "gzip, deflate", r.Headers["accept-encoding"])
	assert.Equal(t, env["QUOTIENT_CHROME_LANGUAGE"], r.Headers["accept-language"])
	assert.Equal(t, fmt.Sprintf("team.example:%d", s.port()), r.Headers["host"])
}

func TestBrowserCurlHTTPSMatchesChrome(t *testing.T) {
	s := startRawServer(t, true)
	env := chromeEnv(t)
	out, code := shEnv(t, env, fmt.Sprintf("browser_curl -s -k https://127.0.0.1:%d/", s.port()))
	require.Equal(t, 0, code)
	assert.Equal(t, "hello GET ", out)
	r := s.take(t)[0]
	assert.Equal(t, []string{"Host", "Connection", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "Upgrade-Insecure-Requests", "User-Agent", "Accept", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-User", "Sec-Fetch-Dest", "Accept-Encoding", "Accept-Language"}, r.Names)
	assert.Equal(t, env["QUOTIENT_CHROME_SEC_CH_UA"], r.Headers["sec-ch-ua"])
	assert.Equal(t, "?0", r.Headers["sec-ch-ua-mobile"])
	assert.Equal(t, env["QUOTIENT_CHROME_PLATFORM"], r.Headers["sec-ch-ua-platform"])
	assert.Equal(t, env["QUOTIENT_CHROME_USER_AGENT"], r.Headers["user-agent"])
	assert.Equal(t, chromeAccept, r.Headers["accept"])
	assert.Equal(t, "none", r.Headers["sec-fetch-site"])
	assert.Equal(t, "?1", r.Headers["sec-fetch-user"])
	assert.Equal(t, env["QUOTIENT_CHROME_LANGUAGE"], r.Headers["accept-language"])
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

// headerCounts counts a request's headers by lower-case name.
func headerCounts(r rawRequest) map[string]int {
	count := map[string]int{}
	for _, n := range r.Names {
		count[strings.ToLower(n)]++
	}
	return count
}

func TestBrowserCurlCheckHeadersWin(t *testing.T) {
	s := startRawServer(t, false)
	_, code := sh(t, fmt.Sprintf("browser_curl -s -A 'probe/1' -H 'Accept: application/json' -H 'Host: wilee.acme.anvil' -H 'Cookie: a=b' http://127.0.0.1:%d/", s.port()))
	require.Equal(t, 0, code)
	r := s.take(t)[0]
	assert.Equal(t, "probe/1", r.Headers["user-agent"])
	assert.Equal(t, "application/json", r.Headers["accept"])
	assert.Equal(t, "wilee.acme.anvil", r.Headers["host"])
	count := headerCounts(r)
	assert.Equal(t, 1, count["accept"])
	assert.Equal(t, 1, count["user-agent"])
	assert.Equal(t, "Host", r.Names[0])
	assert.Equal(t, "Cookie", r.Names[len(r.Names)-1])
}

// The check's headers count in short-option clusters (-sA, -sH, -HName:v) and
// in curl's empty-header form ('Name;').
func TestBrowserCurlCheckHeadersWinInClusters(t *testing.T) {
	s := startRawServer(t, false)
	_, code := sh(t, fmt.Sprintf("browser_curl -sA 'probe/1' -sH 'Accept: application/json' -H 'Accept-Language;' -HUpgrade-Insecure-Requests:0 http://127.0.0.1:%d/", s.port()))
	require.Equal(t, 0, code)
	r := s.take(t)[0]
	count := headerCounts(r)
	for _, h := range []string{"user-agent", "accept", "accept-language", "upgrade-insecure-requests"} {
		assert.Equal(t, 1, count[h], h)
	}
	assert.Equal(t, "probe/1", r.Headers["user-agent"])
	assert.Equal(t, "application/json", r.Headers["accept"])
	assert.Equal(t, "", r.Headers["accept-language"])
	assert.Equal(t, "0", r.Headers["upgrade-insecure-requests"])
}

// Option values that look like URLs (a Referer, POST data) are not the
// request's URL: a plain-http page gets no client hints and no br/zstd, and
// --url's value is the URL. A path or user name that says localhost does not
// make the host trustworthy; a *.localhost host does, as does LOCALHOST.
func TestBrowserCurlTrustworthinessFromTheURLOnly(t *testing.T) {
	s := startRawServer(t, false)
	resolve := fmt.Sprintf("--resolve team.example:%d:127.0.0.1 ", s.port())
	target := resolve + fmt.Sprintf("http://team.example:%d/", s.port())
	for _, c := range []string{
		"-s -e https://ref.example/ " + target,
		"-se https://ref.example/ " + target,
		"-sehttps://ref.example/ " + target,
		"-s --referer https://ref.example/ " + target,
		"-s -d https://data.example/ " + target,
		"-s --data-raw https://data.example/ " + target,
		fmt.Sprintf("-s --referer https://ref.example/ %s--url http://team.example:%d/", resolve, s.port()),
		fmt.Sprintf("-s %shttp://team.example:%d/a.localhost", resolve, s.port()),
		fmt.Sprintf("-s %shttp://localhost@team.example:%d/", resolve, s.port()),
	} {
		_, code := sh(t, "browser_curl "+c)
		require.Equal(t, 0, code, c)
		r := s.take(t)[0]
		assert.NotContains(t, r.Names, "sec-ch-ua", c)
		assert.NotContains(t, r.Names, "Sec-Fetch-Mode", c)
		assert.Equal(t, "gzip, deflate", r.Headers["accept-encoding"], c)
	}

	for _, host := range []string{"team.localhost", "LOCALHOST"} {
		_, code := sh(t, fmt.Sprintf("browser_curl -s --resolve %s:%d:127.0.0.1 http://%s:%d/", host, s.port(), host, s.port()))
		require.Equal(t, 0, code, host)
		assert.Contains(t, s.take(t)[0].Names, "sec-ch-ua", "%s is trustworthy", host)
	}

	tlsServer := startRawServer(t, true)
	_, code := sh(t, fmt.Sprintf("browser_curl -sk -e http://ref.example/ --url https://127.0.0.1:%d/", tlsServer.port()))
	require.Equal(t, 0, code)
	assert.Contains(t, tlsServer.take(t)[0].Names, "sec-ch-ua")
}

// browser-curl.sh reads which options take a value from the curl it runs;
// a curl whose --help all lists none stops the check at source time.
func TestBrowserCurlOptionValuesFromCurlHelp(t *testing.T) {
	out, code := sh(t, `printf '%s|%s' "$_bc_short_values" "$_bc_long_values"`)
	require.Equal(t, 0, code)
	short, long, ok := strings.Cut(out, "|")
	require.True(t, ok, out)
	for _, o := range []string{"H", "e", "x"} {
		assert.Contains(t, short, o, "-%s takes a value", o)
	}
	values := []string{"url", "referer", "data-raw", "header", "proxy"}
	// Before 8.6.0, curl's help left out --haproxy-clientip's argument.
	version, _ := exec.Command("curl", "--version").Output()
	var major, minor int
	_, err := fmt.Sscanf(string(version), "curl %d.%d", &major, &minor)
	require.NoError(t, err)
	if major > 8 || major == 8 && minor >= 6 {
		values = append(values, "haproxy-clientip")
	}
	for _, o := range values {
		assert.Contains(t, long, " "+o+" ", "--%s takes a value", o)
	}
	assert.NotContains(t, short, "s")
	assert.NotContains(t, long, " silent ")

	fake := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fake, "curl"), []byte("#!/bin/sh\n[ \"$1\" = --version ] && echo 'Features: gzip'\nexit 0\n"), 0o755)) // #nosec G306 -- test stub must be executable
	env := chromeEnv(t)
	env["PATH"] = fake + string(os.PathListSeparator) + os.Getenv("PATH")
	out, stderr, code := shRun(t, env, "echo sourced")
	assert.NotEqual(t, 0, code, "sourcing must fail")
	assert.Contains(t, stderr, "curl --help all lists no options")
	assert.Empty(t, out)
}

// A Referer set by the check claims a followed link; browser_curl does not
// invent its site relation, so Sec-Fetch-Site and Sec-Fetch-User stay out.
func TestBrowserCurlRefererDropsSiteAndUser(t *testing.T) {
	s := startRawServer(t, true)
	for _, c := range []string{"-e https://ref.example/", "-H 'Referer: https://ref.example/'"} {
		_, code := sh(t, fmt.Sprintf("browser_curl -sk %s https://127.0.0.1:%d/", c, s.port()))
		require.Equal(t, 0, code, c)
		r := s.take(t)[0]
		assert.Equal(t, "https://ref.example/", r.Headers["referer"], c)
		assert.NotContains(t, r.Names, "Sec-Fetch-Site", c)
		assert.NotContains(t, r.Names, "Sec-Fetch-User", c)
		assert.Contains(t, r.Names, "Sec-Fetch-Mode", c)
		assert.Contains(t, r.Names, "Sec-Fetch-Dest", c)
	}
}

// browser_curl prints the first BROWSER_CURL_MAX_BYTES of the body and exits
// 0, so a regex scores on that prefix; a small gzip response that decodes
// far past the cap ends it quickly.
func TestBrowserCurlPrintsPrefix(t *testing.T) {
	var bomb bytes.Buffer
	zw, err := gzip.NewWriterLevel(&bomb, gzip.BestCompression)
	require.NoError(t, err)
	zeros := make([]byte, 1<<20)
	for range 200 {
		_, err = zw.Write(zeros)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.Less(t, bomb.Len(), 300<<10, "the bomb is small on the wire")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(bomb.Bytes())
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	out, code := sh(t, "browser_curl -s "+srv.URL+"/")
	assert.Less(t, time.Since(start), 20*time.Second)
	assert.Equal(t, 0, code)
	assert.Equal(t, 10<<20, len(out))

	// Just past the cap prints the prefix even when curl finished first; at
	// the cap the page is whole; a cap dd cannot use fails.
	s := startRawServer(t, false)
	out, code = sh(t, fmt.Sprintf("BROWSER_CURL_MAX_BYTES=9; browser_curl -s http://127.0.0.1:%d/", s.port()))
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello GET", out)
	out, code = sh(t, fmt.Sprintf("BROWSER_CURL_MAX_BYTES=10; browser_curl -s http://127.0.0.1:%d/", s.port()))
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello GET ", out)
	out, code = sh(t, fmt.Sprintf("BROWSER_CURL_MAX_BYTES=abc; browser_curl -s http://127.0.0.1:%d/", s.port()))
	assert.NotEqual(t, 0, code)
	assert.Equal(t, "", out)
}

// Under set -e a failing browser_curl stops the check with curl's status;
// browser_curl leaves no files behind, whether curl succeeds, fails or the
// body is cut.
func TestBrowserCurlErrexitAndNoFiles(t *testing.T) {
	s := startRawServer(t, false)
	tmp := t.TempDir()
	env := chromeEnv(t)
	env["TMPDIR"] = tmp

	out, code := shEnv(t, env, "set -e\nx=$(browser_curl -sf http://127.0.0.1:1/)\necho after")
	assert.Equal(t, 7, code, "curl's couldn't-connect status")
	assert.Equal(t, "", out)

	out, code = shEnv(t, env, fmt.Sprintf("set -e\nx=$(browser_curl -sf http://127.0.0.1:%d/fail)\necho after", s.port()))
	assert.Equal(t, 22, code, "curl -f's HTTP error status")
	assert.Equal(t, "", out)

	out, code = shEnv(t, env, fmt.Sprintf("set -e\nx=$(browser_curl -s http://127.0.0.1:%d/)\nprintf '%%s|' \"$x\"\nx=$(BROWSER_CURL_MAX_BYTES=5 browser_curl -s http://127.0.0.1:%d/)\nprintf '%%s' \"$x\"", s.port(), s.port()))
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello GET |hello", out)

	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, entries, "files left in TMPDIR")
}

// A shell check run by the runner claims checks.ChromeMajor; outside the
// runner browser-curl.sh refuses to guess a Chrome and names the variables
// it needs.
func TestBrowserCurlClaimsChromeMajorInCustomCheck(t *testing.T) {
	helper := helperPath(t)
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
	assert.Equal(t, checks.ChromeUserAgent("X11; Linux x86_64"), reqs[0].Headers["user-agent"])

	cmd := exec.Command("sh", "-c", ". "+helper)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "sourcing without the runner's Chrome must fail")
	for _, v := range []string{"QUOTIENT_CHROME_USER_AGENT", "QUOTIENT_CHROME_PLATFORM", "QUOTIENT_CHROME_SEC_CH_UA", "QUOTIENT_CHROME_LANGUAGE"} {
		assert.Contains(t, string(out), v)
	}
}
