package checks

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
)

// chromeClientOSes are the desktop OSes a web check run can claim. The
// User-Agent, the client hints and the sockets' SYN window settings of a run
// all name the same one.
var chromeClientOSes = []clientOS{clientWindows, clientMacOS, clientLinux}

func randomClientOS() clientOS {
	return chromeClientOSes[rand.IntN(len(chromeClientOSes))] // #nosec G404 -- non-crypto selection of the claimed OS
}

// chromeUAOS is the OS part of desktop Chrome's User-Agent on o.
func (o clientOS) chromeUAOS() string {
	switch o {
	case clientWindows:
		return "Windows NT 10.0; Win64; x64"
	case clientMacOS:
		return "Macintosh; Intel Mac OS X 10_15_7"
	}
	return "X11; Linux x86_64"
}

// chromePlatform is desktop Chrome's sec-ch-ua-platform value on o, quoted
// the way Chrome sends it.
func (o clientOS) chromePlatform() string {
	switch o {
	case clientWindows:
		return `"Windows"`
	case clientMacOS:
		return `"macOS"`
	}
	return `"Linux"`
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

const (
	chromeNavigationAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	chromeAcceptLanguage   = "en-US,en;q=0.9"
)

type headerField struct{ name, value string }

// navigationHeaders returns the headers Chrome on o sends when the user types
// the URL into the address bar, in Chrome's HTTP/1.1 wire order and spelling,
// without Host. Client hints, Sec-Fetch-* and br/zstd are only sent to
// potentially trustworthy origins (HTTPS, localhost and loopback addresses).
func navigationHeaders(o clientOS, trustworthy bool) []headerField {
	ua := ChromeUserAgent(o.chromeUAOS())
	if !trustworthy {
		return []headerField{
			{"Connection", "keep-alive"},
			{"Upgrade-Insecure-Requests", "1"},
			{"User-Agent", ua},
			{"Accept", chromeNavigationAccept},
			{"Accept-Encoding", "gzip, deflate"},
			{"Accept-Language", chromeAcceptLanguage},
		}
	}
	return []headerField{
		{"Connection", "keep-alive"},
		{"sec-ch-ua", ChromeSecCHUA(ChromeMajor)},
		{"sec-ch-ua-mobile", "?0"},
		{"sec-ch-ua-platform", o.chromePlatform()},
		{"Upgrade-Insecure-Requests", "1"},
		{"User-Agent", ua},
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

// tlsHandshake runs Chrome's ClientHello over conn. Certificates are not
// verified because competition services use self-signed certificates. Like a
// browser, it sends no SNI when host is an IP address.
func tlsHandshake(ctx context.Context, conn net.Conn, host string) (*utls.UConn, error) {
	cfg := &utls.Config{InsecureSkipVerify: true} // #nosec G402 -- competition services may use self-signed certs
	if net.ParseIP(host) == nil {
		cfg.ServerName = host
	}
	spec, err := chromeHelloSpec()
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
// way desktop Chrome on os does: Chrome's ClientHello, HTTP/2 when the server
// selects it through ALPN, otherwise HTTP/1.1 with Chrome's header order, and
// keep-alive connections that are reused for later requests to the same
// origin (for example a redirect). Every request carries exactly Chrome's
// navigation headers; req.Header is ignored, so a followed redirect carries
// no Referer, as with a typed-in URL. Responses are decoded according to
// Content-Encoding.
//
// One browserTransport serves one check run. Call CloseIdleConnections when
// the run is done.
type browserTransport struct {
	os     clientOS
	dialer *net.Dialer

	mu     sync.Mutex
	idleH1 map[string]*h1Conn
	h2     map[string]*http.ClientConn
}

func newBrowserTransport(o clientOS) *browserTransport {
	return &browserTransport{os: o, dialer: o.dialer(0), idleH1: map[string]*h1Conn{}, h2: map[string]*http.ClientConn{}}
}

// maxResponseHeaderBytes bounds the HTTP/1.1 response heads read for one
// request, as http.Transport's MaxResponseHeaderBytes does.
const maxResponseHeaderBytes = 1 << 20

var errResponseHeaderTooLarge = fmt.Errorf("server response headers exceeded %d bytes; aborted", maxResponseHeaderBytes)

// h1Conn is an HTTP/1.1 connection. br reads conn through h1Conn.Read, which
// fails once readLimit bytes have been read.
type h1Conn struct {
	conn      net.Conn
	br        *bufio.Reader
	readLimit int64
}

func newH1Conn(conn net.Conn) *h1Conn {
	pc := &h1Conn{conn: conn, readLimit: math.MaxInt64}
	pc.br = bufio.NewReader(pc)
	return pc
}

func (pc *h1Conn) Read(p []byte) (int, error) {
	if pc.readLimit <= 0 {
		return 0, errResponseHeaderTooLarge
	}
	if int64(len(p)) > pc.readLimit {
		p = p[:pc.readLimit]
	}
	n, err := pc.conn.Read(p)
	pc.readLimit -= int64(n)
	return n, err
}

func (t *browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody && req.ContentLength != 0 {
		return nil, errors.New("browserTransport sends only requests without a body")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported protocol scheme %q", req.URL.Scheme)
	}
	addr := canonicalAddr(req.URL)
	origin := req.URL.Scheme + "://" + addr
	headers := navigationHeaders(t.os, isTrustworthyOrigin(req.URL))

	if cc := t.takeH2(origin); cc != nil {
		return t.roundTripH2(cc, req, headers)
	}
	if pc := t.takeIdleH1(origin); pc != nil {
		resp, dead, err := t.roundTripH1(pc, origin, req, headers)
		if !dead || req.Context().Err() != nil {
			return resp, err
		}
		// The server closed the idle connection before the request reached
		// it; a browser retries an idempotent request on a new one.
	}

	conn, err := t.dialer.DialContext(req.Context(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme == "https" {
		uc, err := tlsHandshake(req.Context(), conn, req.URL.Hostname())
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if uc.ConnectionState().NegotiatedProtocol == "h2" {
			cc, err := newH2ClientConn(req.Context(), uc, addr)
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
	resp, _, err := t.roundTripH1(newH1Conn(conn), origin, req, headers)
	return resp, err
}

// newH2ClientConn runs HTTP/2 over uc, which negotiated h2. net/http builds
// a ClientConn only over a connection it dials, so the Transport's TLS dialer
// returns uc.
func newH2ClientConn(ctx context.Context, uc *utls.UConn, addr string) (*http.ClientConn, error) {
	cs := uc.ConnectionState()
	conn := h2Conn{Conn: uc, state: tls.ConnectionState{
		Version:            cs.Version,
		HandshakeComplete:  cs.HandshakeComplete,
		CipherSuite:        cs.CipherSuite,
		NegotiatedProtocol: cs.NegotiatedProtocol,
		ServerName:         cs.ServerName,
	}}
	var protocols http.Protocols
	protocols.SetHTTP2(true)
	tr := &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		Protocols:      &protocols,
	}
	return tr.NewClientConn(ctx, "https", addr)
}

// h2Conn carries a uTLS connection's state as a crypto/tls ConnectionState,
// the form in which net/http reads the negotiated protocol.
type h2Conn struct {
	net.Conn
	state tls.ConnectionState
}

func (c h2Conn) ConnectionState() tls.ConnectionState { return c.state }

func (t *browserTransport) takeH2(origin string) *http.ClientConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	cc := t.h2[origin]
	if cc != nil && cc.Available() == 0 {
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

// CloseIdleConnections closes every connection the transport holds; an
// HTTP/2 connection closes at once, even with a response body still being
// read. An HTTP/1.1 connection whose response body is still being read is
// not held: it closes or becomes idle when the body is done.
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

func (t *browserTransport) roundTripH2(cc *http.ClientConn, req *http.Request, headers []headerField) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Host = hostHeader(req)
	r.Header = make(http.Header, len(headers)+1)
	for _, f := range headers {
		if strings.EqualFold(f.name, "Connection") {
			continue // connection-specific headers are not allowed in HTTP/2
		}
		// Add canonicalizes the name so net/http sees our Accept-Encoding
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
	if err := decodeResponse(resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

var aLongTimeAgo = time.Unix(1, 0)

// roundTripH1 sends req over pc. dead reports that pc was already closed by
// the server: the request could not be written, or the connection ended
// before the first byte of a response.
func (t *browserTransport) roundTripH1(pc *h1Conn, origin string, req *http.Request, headers []headerField) (resp *http.Response, dead bool, err error) {
	ctx := req.Context()
	// Unblock reads and writes when the request is cancelled or the client
	// times out, including while the caller reads the body.
	stop := context.AfterFunc(ctx, func() { _ = pc.conn.SetDeadline(aLongTimeAgo) })
	fail := func(dead bool, err error) (*http.Response, bool, error) {
		stop()
		_ = pc.conn.Close()
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, dead, err
	}

	var head bytes.Buffer
	head.WriteString(req.Method + " " + req.URL.RequestURI() + " HTTP/1.1\r\nHost: " + hostHeader(req) + "\r\n")
	for _, f := range headers {
		head.WriteString(f.name + ": " + f.value + "\r\n")
	}
	head.WriteString("\r\n")
	if _, err := pc.conn.Write(head.Bytes()); err != nil {
		return fail(true, err)
	}

	pc.readLimit = maxResponseHeaderBytes
	if _, err := pc.br.Peek(1); err != nil {
		return fail(errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET), err)
	}
	for {
		if resp, err = http.ReadResponse(pc.br, req); err != nil {
			return fail(false, err)
		}
		// Skip informational responses (100 Continue, 103 Early Hints).
		if resp.StatusCode < 100 || resp.StatusCode > 199 || resp.StatusCode == http.StatusSwitchingProtocols {
			break
		}
	}
	pc.readLimit = math.MaxInt64

	resp.Body = &h1Body{ReadCloser: resp.Body, done: func(reusable bool) {
		if stop() && reusable && !resp.Close {
			t.putIdleH1(origin, pc)
			return
		}
		_ = pc.conn.Close()
	}}
	if err := decodeResponse(resp); err != nil {
		_ = resp.Body.Close()
		return nil, false, err
	}
	return resp, false, nil
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

// maxContentCodings is how many codings a Content-Encoding may chain. Each
// coding multiplies how far a small response can expand.
const maxContentCodings = 2

// zstdMaxWindow is the largest zstd window RFC 9659 lets HTTP content use.
const zstdMaxWindow = 8 << 20

// contentDecoders undo the Content-Encodings Chrome accepts.
var contentDecoders = map[string]func(io.Reader) (io.ReadCloser, error){
	"gzip":    func(r io.Reader) (io.ReadCloser, error) { return gzip.NewReader(r) },
	"deflate": newDeflateReader,
	"br":      func(r io.Reader) (io.ReadCloser, error) { return io.NopCloser(brotli.NewReader(r)), nil },
	"zstd":    newZstdReader,
}

// newDeflateReader reads zlib-wrapped DEFLATE, which "deflate" names, and the
// raw DEFLATE some servers send instead; browsers accept both.
func newDeflateReader(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	if hdr, err := br.Peek(2); err == nil && hdr[0]&0x0f == 8 && (uint16(hdr[0])<<8|uint16(hdr[1]))%31 == 0 {
		return zlib.NewReader(br)
	}
	return flate.NewReader(br), nil
}

func newZstdReader(r io.Reader) (io.ReadCloser, error) {
	zr, err := zstd.NewReader(r,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxWebBodyBytes),
		zstd.WithDecoderMaxWindow(zstdMaxWindow))
	if err != nil {
		return nil, err
	}
	return zr.IOReadCloser(), nil
}

// decodeResponse replaces resp.Body with a reader that undoes the response's
// Content-Encoding, as a browser does. A list naming a coding this package
// does not know is left in place. A list of more than maxContentCodings
// codings, or naming one twice, is an error.
func decodeResponse(resp *http.Response) error {
	ce := resp.Header.Get("Content-Encoding")
	var codings []string
	for _, c := range strings.Split(ce, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "x-gzip" {
			c = "gzip"
		}
		if c == "" || c == "identity" {
			continue
		}
		if _, ok := contentDecoders[c]; !ok {
			return nil
		}
		if slices.Contains(codings, c) {
			return fmt.Errorf("response Content-Encoding %q applies %s twice", ce, c)
		}
		codings = append(codings, c)
	}
	if len(codings) > maxContentCodings {
		return fmt.Errorf("response Content-Encoding %q chains more than %d codings", ce, maxContentCodings)
	}
	if len(codings) == 0 {
		return nil
	}
	resp.Body = &decodedBody{src: resp.Body, codings: codings}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return nil
}

// decodedBody builds its decoders on the first Read so an empty body (a 204
// or 304 that still names a coding) does not fail.
type decodedBody struct {
	src     io.ReadCloser
	codings []string
	r       io.Reader
	closers []io.Closer
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
		dr, err := contentDecoders[d.codings[i]](r)
		if err != nil {
			return nil, err
		}
		d.closers = append(d.closers, dr)
		r = dr
	}
	return r, nil
}

func (d *decodedBody) Close() error {
	for _, c := range d.closers {
		_ = c.Close()
	}
	return d.src.Close()
}
