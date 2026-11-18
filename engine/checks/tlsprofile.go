package checks

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// A browserProfile ties together everything a check sends that a browser
// would also send: the User-Agent, the client hints, the request headers in
// wire order, and the TLS ClientHello. Each part must come from the same
// browser build; a Chrome User-Agent over a Go ClientHello with Go's header
// order is easier to spot than any one of them alone.
type browserProfile struct {
	name      string
	platform  string // sec-ch-ua-platform value, quoted the way Chrome sends it
	uaOS      string // the OS part of the User-Agent
	helloSpec func() (*utls.ClientHelloSpec, error)
}

// chromeMajor is the Chrome release the profiles claim to be. Keep it at the
// current stable release: Chrome ships every four weeks, and a version months
// behind is itself unusual.
const chromeMajor = 158

var browserProfiles = []browserProfile{
	{name: "chrome-windows", platform: `"Windows"`, uaOS: "Windows NT 10.0; Win64; x64", helloSpec: chromeHelloSpec},
	{name: "chrome-macos", platform: `"macOS"`, uaOS: "Macintosh; Intel Mac OS X 10_15_7", helloSpec: chromeHelloSpec},
	{name: "chrome-linux", platform: `"Linux"`, uaOS: "X11; Linux x86_64", helloSpec: chromeHelloSpec},
}

// chromeMLDSASignatureSchemes are the ML-DSA-44/65/87 signature algorithms
// Chrome lists first; uTLS's Chrome 133 template predates them.
var chromeMLDSASignatureSchemes = []utls.SignatureScheme{0x0904, 0x0905, 0x0906}

// chromeHelloSpec is uTLS's Chrome 133 ClientHello with the ML-DSA signature
// algorithms added in front, which gives the JA4 of Chrome 151
// (t13i1515h2_8daaf6152771_806a8c22fdea when no SNI is sent). Build it for
// every connection: uTLS shuffles Chrome's extension order while building the
// spec, as Chrome does for each connection.
func chromeHelloSpec() (*utls.ClientHelloSpec, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_133)
	if err != nil {
		return nil, err
	}
	for _, ext := range spec.Extensions {
		if sa, ok := ext.(*utls.SignatureAlgorithmsExtension); ok {
			sa.SupportedSignatureAlgorithms = append(slices.Clone(chromeMLDSASignatureSchemes), sa.SupportedSignatureAlgorithms...)
		}
	}
	return &spec, nil
}

func randomBrowserProfile() browserProfile {
	return browserProfiles[rand.IntN(len(browserProfiles))] // #nosec G404 -- non-crypto selection of a browser profile
}

// userAgent is the reduced User-Agent Chrome sends since version 110: the
// minor, build and patch numbers are always zero.
func (p browserProfile) userAgent() string {
	return "Mozilla/5.0 (" + p.uaOS + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + strconv.Itoa(chromeMajor) + ".0.0.0 Safari/537.36"
}

// chromeSecCHUA builds the sec-ch-ua value the way Chromium does
// (GetUserAgentBrandList in components/embedder_support/user_agent_utils.cc):
// the GREASE brand, its version and the brand order all derive from the major
// version.
func chromeSecCHUA(major int) string {
	greaseyChars := []string{" ", "(", ":", "-", ".", "/", ")", ";", "=", "?", "_"}
	greasedVersions := []string{"8", "99", "24"}
	orders := [6][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	v := strconv.Itoa(major)
	grease := `"Not` + greaseyChars[major%len(greaseyChars)] + "A" + greaseyChars[(major+1)%len(greaseyChars)] + `Brand";v="` + greasedVersions[major%len(greasedVersions)] + `"`
	var brands [3]string
	order := orders[major%len(orders)]
	brands[order[0]] = grease
	brands[order[1]] = `"Chromium";v="` + v + `"`
	brands[order[2]] = `"Chrome";v="` + v + `"`
	return strings.Join(brands[:], ", ")
}

const (
	chromeNavigationAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	chromeAcceptLanguage   = "en-US,en;q=0.9"
)

type headerField struct{ name, value string }

// navigationHeaders returns the headers Chrome sends when the user types the
// URL into the address bar, in Chrome's HTTP/1.1 wire order and spelling,
// without Host. Client hints, Sec-Fetch-* and br/zstd are only sent to
// potentially trustworthy origins (HTTPS, localhost and loopback addresses).
func (p browserProfile) navigationHeaders(trustworthy bool) []headerField {
	if !trustworthy {
		return []headerField{
			{"Connection", "keep-alive"},
			{"Upgrade-Insecure-Requests", "1"},
			{"User-Agent", p.userAgent()},
			{"Accept", chromeNavigationAccept},
			{"Accept-Encoding", "gzip, deflate"},
			{"Accept-Language", chromeAcceptLanguage},
		}
	}
	return []headerField{
		{"Connection", "keep-alive"},
		{"sec-ch-ua", chromeSecCHUA(chromeMajor)},
		{"sec-ch-ua-mobile", "?0"},
		{"sec-ch-ua-platform", p.platform},
		{"Upgrade-Insecure-Requests", "1"},
		{"User-Agent", p.userAgent()},
		{"Accept", chromeNavigationAccept},
		{"Sec-Fetch-Site", "none"},
		{"Sec-Fetch-Mode", "navigate"},
		{"Sec-Fetch-User", "?1"},
		{"Sec-Fetch-Dest", "document"},
		{"Accept-Encoding", "gzip, deflate, br, zstd"},
		{"Accept-Language", chromeAcceptLanguage},
	}
}

// isTrustworthyOrigin reports whether Chrome treats the URL's origin as
// potentially trustworthy (W3C Secure Contexts).
func isTrustworthyOrigin(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tlsHandshake runs the profile's ClientHello over conn. Certificates are not
// verified because competition services use self-signed certificates. Like a
// browser, it sends no SNI when host is an IP address.
func (p browserProfile) tlsHandshake(ctx context.Context, conn net.Conn, host string) (*utls.UConn, error) {
	cfg := &utls.Config{InsecureSkipVerify: true} // #nosec G402 -- competition services may use self-signed certs
	if net.ParseIP(host) == nil {
		cfg.ServerName = host
	}
	spec, err := p.helloSpec()
	if err != nil {
		return nil, err
	}
	uc := utls.UClient(conn, cfg, utls.HelloCustom)
	if err := uc.ApplyPreset(spec); err != nil {
		return nil, err
	}
	if err := uc.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return uc, nil
}

// browserTransport is an http.RoundTripper that sends body-less requests the
// way the profile's browser does: its ClientHello, HTTP/2 when the server
// selects it through ALPN, otherwise HTTP/1.1 with the browser's header order,
// and keep-alive connections that are reused for later requests to the same
// origin (for example a redirect). Responses are decoded according to
// Content-Encoding. Headers in the request that the profile does not define
// are sent after the profile's headers.
//
// One browserTransport serves one check run. Call CloseIdleConnections when
// the run is done.
type browserTransport struct {
	profile browserProfile
	dialer  net.Dialer
	h2t     http2.Transport

	mu     sync.Mutex
	idleH1 map[string]*h1Conn
	h2     map[string]*http2.ClientConn
}

func newBrowserTransport(p browserProfile) *browserTransport {
	return &browserTransport{profile: p, idleH1: map[string]*h1Conn{}, h2: map[string]*http2.ClientConn{}}
}

type h1Conn struct {
	conn net.Conn
	br   *bufio.Reader
}

var errRequestBody = errors.New("browserTransport sends only requests without a body")

func (t *browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody && req.ContentLength != 0 {
		return nil, errRequestBody
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported protocol scheme %q", req.URL.Scheme)
	}
	addr := canonicalAddr(req.URL)
	origin := req.URL.Scheme + "://" + addr
	headers := t.requestHeaders(req)

	if cc := t.takeH2(origin); cc != nil {
		return t.roundTripH2(cc, req, headers)
	}
	if pc := t.takeIdleH1(origin); pc != nil {
		resp, err := t.roundTripH1(pc, origin, req, headers)
		if err == nil || req.Context().Err() != nil {
			return resp, err
		}
		// The server may have closed the idle connection just before we
		// reused it; a browser retries an idempotent request on a new one.
	}

	conn, err := t.dialer.DialContext(req.Context(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme == "https" {
		uc, err := t.profile.tlsHandshake(req.Context(), conn, req.URL.Hostname())
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if uc.ConnectionState().NegotiatedProtocol == "h2" {
			cc, err := t.h2t.NewClientConn(uc)
			if err != nil {
				_ = uc.Close()
				return nil, err
			}
			t.mu.Lock()
			t.h2[origin] = cc
			t.mu.Unlock()
			return t.roundTripH2(cc, req, headers)
		}
		conn = uc
	}
	return t.roundTripH1(&h1Conn{conn: conn, br: bufio.NewReader(conn)}, origin, req, headers)
}

// requestHeaders is the profile's header list followed by any other request
// headers, sorted, with Host left out.
func (t *browserTransport) requestHeaders(req *http.Request) []headerField {
	headers := t.profile.navigationHeaders(isTrustworthyOrigin(req.URL))
	var extra []string
	for name := range req.Header {
		if strings.EqualFold(name, "Host") || slices.ContainsFunc(headers, func(f headerField) bool { return strings.EqualFold(f.name, name) }) {
			continue
		}
		extra = append(extra, name)
	}
	slices.Sort(extra)
	for _, name := range extra {
		for _, v := range req.Header[name] {
			headers = append(headers, headerField{name, v})
		}
	}
	return headers
}

func (t *browserTransport) takeH2(origin string) *http2.ClientConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	cc := t.h2[origin]
	if cc != nil && !cc.CanTakeNewRequest() {
		delete(t.h2, origin)
		_ = cc.Close()
		return nil
	}
	return cc
}

func (t *browserTransport) takeIdleH1(origin string) *h1Conn {
	t.mu.Lock()
	defer t.mu.Unlock()
	pc := t.idleH1[origin]
	delete(t.idleH1, origin)
	return pc
}

func (t *browserTransport) putIdleH1(origin string, pc *h1Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old := t.idleH1[origin]; old != nil {
		_ = old.conn.Close()
	}
	t.idleH1[origin] = pc
}

// CloseIdleConnections closes every connection the transport holds. A
// response body still being read keeps its connection until it is closed.
func (t *browserTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for origin, pc := range t.idleH1 {
		_ = pc.conn.Close()
		delete(t.idleH1, origin)
	}
	for origin, cc := range t.h2 {
		_ = cc.Close()
		delete(t.h2, origin)
	}
}

func (t *browserTransport) roundTripH2(cc *http2.ClientConn, req *http.Request, headers []headerField) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Host = hostHeader(req)
	r.Header = make(http.Header, len(headers)+1)
	for _, f := range headers {
		if strings.EqualFold(f.name, "Connection") {
			continue // connection-specific headers are not allowed in HTTP/2
		}
		// Add canonicalizes the name so x/net/http2 sees our Accept-Encoding
		// and does not add its own; HTTP/2 sends every name in lower case.
		r.Header.Add(f.name, f.value)
	}
	// Chrome sends the RFC 9218 priority of a navigation over HTTP/2 only.
	r.Header.Add("Priority", "u=0, i")
	resp, err := cc.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Request = req
	decodeResponse(resp)
	return resp, nil
}

var aLongTimeAgo = time.Unix(1, 0)

func (t *browserTransport) roundTripH1(pc *h1Conn, origin string, req *http.Request, headers []headerField) (*http.Response, error) {
	ctx := req.Context()
	// Unblock reads and writes when the request is cancelled or the client
	// times out, including while the caller reads the body.
	stop := context.AfterFunc(ctx, func() { _ = pc.conn.SetDeadline(aLongTimeAgo) })
	fail := func(err error) (*http.Response, error) {
		stop()
		_ = pc.conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	var head bytes.Buffer
	head.WriteString(req.Method + " " + req.URL.RequestURI() + " HTTP/1.1\r\nHost: " + hostHeader(req) + "\r\n")
	for _, f := range headers {
		head.WriteString(f.name + ": " + f.value + "\r\n")
	}
	head.WriteString("\r\n")
	if _, err := pc.conn.Write(head.Bytes()); err != nil {
		return fail(err)
	}

	var resp *http.Response
	for {
		var err error
		if resp, err = http.ReadResponse(pc.br, req); err != nil {
			return fail(err)
		}
		// Skip informational responses (100 Continue, 103 Early Hints).
		if resp.StatusCode < 100 || resp.StatusCode > 199 || resp.StatusCode == http.StatusSwitchingProtocols {
			break
		}
	}

	resp.Body = &h1Body{ReadCloser: resp.Body, done: func(reusable bool) {
		if stop() && reusable && !resp.Close {
			t.putIdleH1(origin, pc)
			return
		}
		_ = pc.conn.Close()
	}}
	decodeResponse(resp)
	return resp, nil
}

// h1Body hands its connection back as soon as the body has been read to the
// end (before http.Client cancels the request context on EOF), and closes the
// connection if the body is closed early instead of draining it.
type h1Body struct {
	io.ReadCloser
	once sync.Once
	done func(reusable bool)
}

func (b *h1Body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(func() { b.done(true) })
	}
	return n, err
}

func (b *h1Body) Close() error {
	early := false
	b.once.Do(func() { early = true; b.done(false) })
	err := b.ReadCloser.Close()
	if early {
		return nil // the connection was closed on purpose; the drain error is expected
	}
	return err
}

// canonicalAddr is the URL's host:port with the scheme's default port filled in.
func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// hostHeader is the Host a browser sends: the scheme's default port is left out.
func hostHeader(req *http.Request) string {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if h, port, err := net.SplitHostPort(host); err == nil &&
		((req.URL.Scheme == "http" && port == "80") || (req.URL.Scheme == "https" && port == "443")) {
		if strings.Contains(h, ":") {
			return "[" + h + "]"
		}
		return h
	}
	return host
}

// decodeResponse replaces resp.Body with a reader that undoes the response's
// Content-Encoding, as a browser does. Codings this package does not know are
// left in place.
func decodeResponse(resp *http.Response) {
	ce := resp.Header.Get("Content-Encoding")
	if ce == "" {
		return
	}
	var codings []string
	for _, c := range strings.Split(ce, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		switch c {
		case "", "identity":
		case "gzip", "x-gzip", "deflate", "br", "zstd":
			codings = append(codings, c)
		default:
			return
		}
	}
	if len(codings) == 0 {
		return
	}
	resp.Body = &decodedBody{src: resp.Body, codings: codings}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
}

// decodedBody builds its decoders on the first Read so an empty body (a 204
// or 304 that still names a coding) does not fail.
type decodedBody struct {
	src     io.ReadCloser
	codings []string
	r       io.Reader
	closers []func()
	err     error
}

func (d *decodedBody) Read(p []byte) (int, error) {
	if d.r == nil && d.err == nil {
		d.r, d.err = d.build()
	}
	if d.err != nil {
		return 0, d.err
	}
	return d.r.Read(p)
}

func (d *decodedBody) build() (io.Reader, error) {
	var r io.Reader = d.src
	// Codings are listed in the order they were applied.
	for i := len(d.codings) - 1; i >= 0; i-- {
		switch d.codings[i] {
		case "gzip", "x-gzip":
			gz, err := gzip.NewReader(r)
			if err != nil {
				return nil, err
			}
			r = gz
		case "deflate":
			// "deflate" should be zlib-wrapped, but some servers send raw
			// DEFLATE; browsers accept both.
			br := bufio.NewReader(r)
			if hdr, err := br.Peek(2); err == nil && hdr[0]&0x0f == 8 && (uint16(hdr[0])<<8|uint16(hdr[1]))%31 == 0 {
				zr, err := zlib.NewReader(br)
				if err != nil {
					return nil, err
				}
				r = zr
			} else {
				fr := flate.NewReader(br)
				d.closers = append(d.closers, func() { _ = fr.Close() })
				r = fr
			}
		case "br":
			r = brotli.NewReader(r)
		case "zstd":
			zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, err
			}
			d.closers = append(d.closers, zr.Close)
			r = zr
		}
	}
	return r, nil
}

func (d *decodedBody) Close() error {
	for _, c := range d.closers {
		c()
	}
	return d.src.Close()
}
