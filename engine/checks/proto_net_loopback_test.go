package checks

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- NTLMv2 uses HMAC-MD5
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/md4" // #nosec G501 -- NTLM's NT hash is MD4
)

// Loopback servers for the network checks (dns, tcp, rdp, ldap, smb). Each one records what the
// client sent so the tests can assert both the scoring result and the wire behaviour.

type runnable interface {
	Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result)
}

func runCheck(t *testing.T, c runnable) Result {
	t.Helper()
	ch := make(chan Result, 1)
	c.Run(1, "01", 1, ch)
	select {
	case r := <-ch:
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("check did not return")
		return Result{}
	}
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	tcp, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		udp, err := net.ResolveUDPAddr("udp", addr)
		require.NoError(t, err)
		return udp.IP.String(), udp.Port
	}
	return tcp.IP.String(), tcp.Port
}

// closedPort returns a loopback port with no listener.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port := hostPort(t, ln.Addr().String())
	require.NoError(t, ln.Close())
	return port
}

// ---------------------------------------------------------------- dns

type dnsQuery struct {
	Name  string
	Qtype uint16
	Peer  string
}

type fakeDNS struct {
	mu      sync.Mutex
	queries []dnsQuery
	answers map[string]string // lower-case fqdn -> A
	silent  bool              // never answer
}

func (f *fakeDNS) ServeDNS(w dns.ResponseWriter, m *dns.Msg) {
	q := m.Question[0]
	f.mu.Lock()
	f.queries = append(f.queries, dnsQuery{Name: q.Name, Qtype: q.Qtype, Peer: w.RemoteAddr().String()})
	ip, ok := f.answers[strings.ToLower(q.Name)]
	silent := f.silent
	f.mu.Unlock()
	if silent {
		return
	}
	resp := new(dns.Msg)
	resp.SetReply(m)
	if ok && q.Qtype == dns.TypeA {
		resp.Answer = append(resp.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(ip)})
	}
	if !ok {
		resp.Rcode = dns.RcodeNameError
	}
	_ = w.WriteMsg(resp)
}

func (f *fakeDNS) snapshot() []dnsQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dnsQuery(nil), f.queries...)
}

func startDNS(t *testing.T, f *fakeDNS) (string, int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &dns.Server{PacketConn: pc, Handler: f}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return hostPort(t, pc.LocalAddr().String())
}

func newDnsCheck(host string, port int, kind, domain string, answers ...string) *Dns {
	return &Dns{
		Service: Service{Name: "test-dns", Target: host, Port: port, Timeout: 3},
		Record:  []DnsRecord{{Kind: kind, Domain: domain, Answer: answers}},
	}
}

func TestDnsRun_Loopback(t *testing.T) {
	f := &fakeDNS{answers: map[string]string{"www.team01.acme.local.": "192.0.2.10"}}
	host, port := startDNS(t, f)

	t.Run("correct answer", func(t *testing.T) {
		r := runCheck(t, newDnsCheck(host, port, "A", "www.team_.acme.local", "192.0.2.10"))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
	})
	t.Run("team placeholder in answer", func(t *testing.T) {
		r := runCheck(t, newDnsCheck(host, port, "A", "www.team_.acme.local", "192.0.2.99", "192.0.2.10"))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
	})
	t.Run("wrong answer", func(t *testing.T) {
		r := runCheck(t, newDnsCheck(host, port, "A", "www.team_.acme.local", "192.0.2.11"))
		assert.False(t, r.Status)
		assert.Equal(t, "incorrect answer(s) received from DNS", r.Error)
	})
	t.Run("nxdomain", func(t *testing.T) {
		r := runCheck(t, newDnsCheck(host, port, "A", "missing.acme.local", "192.0.2.10"))
		assert.False(t, r.Status)
		assert.Equal(t, "no records received", r.Error)
	})
	t.Run("mx answers never match", func(t *testing.T) {
		r := runCheck(t, newDnsCheck(host, port, "MX", "www.team_.acme.local", "192.0.2.10"))
		assert.False(t, r.Status)
		assert.Equal(t, "no records received", r.Error)
	})
	t.Run("no response", func(t *testing.T) {
		// The query and its retry each wait Timeout-1 s, so the service timeout fires first.
		quiet := &fakeDNS{silent: true}
		qh, qp := startDNS(t, quiet)
		r := runCheck(t, newDnsCheck(qh, qp, "A", "www.team_.acme.local", "192.0.2.10"))
		assert.False(t, r.Status)
		assert.Equal(t, "check timeout exceeded", r.Error)
		assert.NotEmpty(t, quiet.snapshot())
	})
}

// ---------------------------------------------------------------- tcp / rdp

type tcpFlow struct {
	ClientBytes int
	Closed      string // "fin", "rst" or "open" (still open when the server gave up)
	CloseAfter  time.Duration
}

// startTCPSink accepts connections and reports, per connection, what the client sent and how it closed.
func startTCPSink(t *testing.T, wait time.Duration) (string, int, <-chan tcpFlow) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	flows := make(chan tcpFlow, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				start := time.Now()
				_ = c.SetReadDeadline(start.Add(wait))
				n, err := io.Copy(io.Discard, c)
				f := tcpFlow{ClientBytes: int(n), CloseAfter: time.Since(start)}
				var ne net.Error
				switch {
				case err == nil:
					f.Closed = "fin"
				case errors.As(err, &ne) && ne.Timeout():
					f.Closed = "open"
				default:
					f.Closed = "rst"
				}
				flows <- f
			}(c)
		}
	}()
	host, port := hostPort(t, ln.Addr().String())
	return host, port, flows
}

func TestTcpRun_Loopback(t *testing.T) {
	host, port, _ := startTCPSink(t, 2*time.Second)
	t.Run("open", func(t *testing.T) {
		r := runCheck(t, &Tcp{Service: Service{Name: "test-tcp", Target: host, Port: port, Timeout: 3}})
		assert.True(t, r.Status, r.Debug)
		assert.Equal(t, "responded to request", r.Debug)
	})
	t.Run("closed", func(t *testing.T) {
		r := runCheck(t, &Tcp{Service: Service{Name: "test-tcp", Target: "127.0.0.1", Port: closedPort(t), Timeout: 3}})
		assert.False(t, r.Status)
		assert.Equal(t, "connection error", r.Error)
	})
}

func TestRdpRun_Loopback(t *testing.T) {
	host, port, _ := startTCPSink(t, 2*time.Second)
	t.Run("open", func(t *testing.T) {
		r := runCheck(t, &Rdp{Service: Service{Name: "test-rdp", Target: host, Port: port, Timeout: 3}})
		assert.True(t, r.Status, r.Debug)
	})
	t.Run("closed", func(t *testing.T) {
		r := runCheck(t, &Rdp{Service: Service{Name: "test-rdp", Target: "127.0.0.1", Port: closedPort(t), Timeout: 3}})
		assert.False(t, r.Status)
		assert.Equal(t, "connection error", r.Error)
	})
}

// ---------------------------------------------------------------- ldap

type berTLV struct {
	tag  byte
	body []byte
}

func readBER(r io.Reader) (berTLV, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(r, h); err != nil {
		return berTLV{}, err
	}
	l := int(h[1])
	if l&0x80 != 0 {
		lb := make([]byte, l&0x7f)
		if _, err := io.ReadFull(r, lb); err != nil {
			return berTLV{}, err
		}
		l = 0
		for _, b := range lb {
			l = l<<8 | int(b)
		}
	}
	body := make([]byte, l)
	_, err := io.ReadFull(r, body)
	return berTLV{h[0], body}, err
}

func splitBER(b []byte) []berTLV {
	var out []berTLV
	r := bytes.NewReader(b)
	for r.Len() > 0 {
		t, err := readBER(r)
		if err != nil {
			break
		}
		out = append(out, t)
	}
	return out
}

type fakeLDAP struct {
	user, password string // accepted simple bind
	mu             sync.Mutex
	ops            []string // per connection, ops joined by ","; "|fin" or "|rst" appended at close
}

func (f *fakeLDAP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var ops []string
	end := "fin"
	for {
		msg, err := readBER(c)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				end = "rst"
			}
			break
		}
		parts := splitBER(msg.body)
		if len(parts) < 2 {
			break
		}
		id := parts[0].body
		op := parts[1]
		switch op.tag {
		case 0x60: // BindRequest
			ops = append(ops, "bind")
			fields := splitBER(op.body)
			code := byte(49) // invalidCredentials
			if len(fields) >= 3 && string(fields[1].body) == f.user && fields[2].tag == 0x80 && string(fields[2].body) == f.password {
				code = 0
			}
			res := []byte{0x0a, 0x01, code, 0x04, 0x00, 0x04, 0x00}
			body := append([]byte{0x02, byte(len(id))}, id...)
			body = append(body, 0x61, byte(len(res)))
			body = append(body, res...)
			_, _ = c.Write(append([]byte{0x30, byte(len(body))}, body...))
		case 0x42: // UnbindRequest
			ops = append(ops, "unbind")
		default:
			ops = append(ops, "other")
		}
	}
	f.mu.Lock()
	f.ops = append(f.ops, strings.Join(ops, ",")+"|"+end)
	f.mu.Unlock()
}

func (f *fakeLDAP) last(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.ops)
		var s string
		if n > 0 {
			s = f.ops[n-1]
		}
		f.mu.Unlock()
		if n > 0 {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no ldap connection recorded")
	return ""
}

func startLDAP(t *testing.T, f *fakeLDAP) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return hostPort(t, ln.Addr().String())
}

func newLdapCheck(host string, port int, domain, user, password string) *Ldap {
	c := &Ldap{Service: Service{Name: "test-ldap", Target: host, Port: port, Timeout: 3, CredLists: []string{"users"}}, Domain: domain}
	c.SetTaskCredentials([]TaskCredential{{Credlist: "users", Username: user, Password: password}})
	return c
}

func TestLdapRun_Loopback(t *testing.T) {
	f := &fakeLDAP{user: "bugs.bunny@acme.local", password: "Carrots1!"}
	host, port := startLDAP(t, f)

	t.Run("bind accepted", func(t *testing.T) {
		r := runCheck(t, newLdapCheck(host, port, "acme.local", "bugs.bunny", "Carrots1!"))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
	})
	t.Run("bind rejected", func(t *testing.T) {
		r := runCheck(t, newLdapCheck(host, port, "acme.local", "bugs.bunny", "wrong"))
		assert.False(t, r.Status)
		assert.Equal(t, "login failed for bugs.bunny", r.Error)
	})
	t.Run("bad domain", func(t *testing.T) {
		r := runCheck(t, newLdapCheck(host, port, "acme", "bugs.bunny", "Carrots1!"))
		assert.False(t, r.Status)
		assert.Equal(t, "Configured domain is not valid (needs to be domain and tld)", r.Error)
	})
	t.Run("connection refused", func(t *testing.T) {
		r := runCheck(t, newLdapCheck("127.0.0.1", closedPort(t), "acme.local", "bugs.bunny", "Carrots1!"))
		assert.False(t, r.Status)
		assert.Equal(t, "failed to connect", r.Error)
	})
}

// ---------------------------------------------------------------- smb

const (
	smbStatusSuccess         = 0x00000000
	smbStatusMoreProcessing  = 0xC0000016
	smbStatusLogonFailure    = 0xC000006D
	smbStatusBadNetworkName  = 0xC00000CC
	smbStatusObjectNotFound  = 0xC0000034
	smbStatusEndOfFile       = 0xC0000011
	smbSessionFlagIsGuest    = 0x0001
	ntlmNegotiateTargetInfo  = 0x00800000
	smbFakeServerChallenge   = "\x01\x23\x45\x67\x89\xab\xcd\xef"
	smbFakeNetbiosDomainName = "TELLS"
)

// smbSession is what the fake SMB server saw on one connection.
type smbSession struct {
	User        string
	Domain      string
	Workstation string
	AvPairs     map[uint16][]byte // NTLMv2 client blob AV pairs
	TreePaths   []string
	Commands    []string
}

// fakeSMB is a minimal SMB 2.1 file server: NTLMv2 logon (verified against the password), one
// disk share, read-only files. It flags sessions as guest so go-smb2 does not sign, which keeps
// the server small; authentication is still checked.
type fakeSMB struct {
	user, password string
	share          string
	files          map[string]string
	mu             sync.Mutex
	sessions       []smbSession
}

func u16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return b
}

func fromU16le(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

func ntlmField(msg []byte, at int) []byte {
	if at+8 > len(msg) {
		return nil
	}
	l := int(binary.LittleEndian.Uint16(msg[at:]))
	off := int(binary.LittleEndian.Uint32(msg[at+4:]))
	if off+l > len(msg) {
		return nil
	}
	return msg[off : off+l]
}

func hmacMD5(key []byte, data ...[]byte) []byte {
	h := hmac.New(md5.New, key)
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

func ntlmChallenge(clientFlags uint32) []byte {
	target := u16le(smbFakeNetbiosDomainName)
	var info []byte
	av := func(id uint16, v []byte) {
		info = binary.LittleEndian.AppendUint16(info, id)
		info = binary.LittleEndian.AppendUint16(info, uint16(len(v)))
		info = append(info, v...)
	}
	av(2, target)
	av(1, u16le("FAKESMB"))
	ts := make([]byte, 8)
	binary.LittleEndian.PutUint64(ts, uint64(time.Now().UnixNano()/100+116444736000000000))
	av(7, ts)
	av(0, nil)

	msg := make([]byte, 56)
	copy(msg, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(msg[8:], 2)
	binary.LittleEndian.PutUint16(msg[12:], uint16(len(target)))
	binary.LittleEndian.PutUint16(msg[14:], uint16(len(target)))
	binary.LittleEndian.PutUint32(msg[16:], 56)
	binary.LittleEndian.PutUint32(msg[20:], clientFlags|ntlmNegotiateTargetInfo)
	copy(msg[24:32], smbFakeServerChallenge)
	binary.LittleEndian.PutUint16(msg[40:], uint16(len(info)))
	binary.LittleEndian.PutUint16(msg[42:], uint16(len(info)))
	binary.LittleEndian.PutUint32(msg[44:], uint32(56+len(target)))
	copy(msg[48:], []byte{10, 0, 0x61, 0x4a, 0, 0, 0, 15})
	msg = append(msg, target...)
	return append(msg, info...)
}

// verifyNTLMv2 checks an AUTHENTICATE message against the configured user/password and records its fields.
func (f *fakeSMB) verifyNTLMv2(auth []byte, s *smbSession) bool {
	nt := ntlmField(auth, 20)
	s.Domain = fromU16le(ntlmField(auth, 28))
	s.User = fromU16le(ntlmField(auth, 36))
	s.Workstation = fromU16le(ntlmField(auth, 44))
	if len(nt) < 16+28 {
		return false
	}
	s.AvPairs = map[uint16][]byte{}
	for av := nt[16+28:]; len(av) >= 4; {
		id := binary.LittleEndian.Uint16(av)
		l := int(binary.LittleEndian.Uint16(av[2:]))
		if id == 0 || 4+l > len(av) {
			break
		}
		s.AvPairs[id] = av[4 : 4+l]
		av = av[4+l:]
	}
	h := md4.New()
	h.Write(u16le(f.password))
	ntowf := hmacMD5(h.Sum(nil), u16le(strings.ToUpper(s.User)+s.Domain))
	proof := hmacMD5(ntowf, []byte(smbFakeServerChallenge), nt[16:])
	return s.User == f.user && hmac.Equal(proof, nt[:16])
}

type spnegoRespToken struct {
	NegState      asn1.Enumerated       `asn1:"explicit,tag:0"`
	SupportedMech asn1.ObjectIdentifier `asn1:"explicit,optional,tag:1"`
	ResponseToken []byte                `asn1:"explicit,optional,tag:2"`
}

func spnegoResp(state asn1.Enumerated, token []byte) []byte {
	tok := spnegoRespToken{NegState: state}
	if token != nil {
		tok.SupportedMech = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 2, 10}
		tok.ResponseToken = token
	}
	seq, err := asn1.Marshal(tok)
	if err != nil {
		panic(err)
	}
	out, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: seq})
	if err != nil {
		panic(err)
	}
	return out
}

var smbCommandNames = map[uint16]string{0: "NEGOTIATE", 1: "SESSION_SETUP", 2: "LOGOFF", 3: "TREE_CONNECT", 4: "TREE_DISCONNECT", 5: "CREATE", 6: "CLOSE", 8: "READ", 16: "QUERY_INFO"}

func (f *fakeSMB) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	var s smbSession
	defer func() {
		f.mu.Lock()
		f.sessions = append(f.sessions, s)
		f.mu.Unlock()
	}()
	const sessionID = 0x1122334455667788
	openFiles := map[uint64]string{}
	var nextFile uint64 = 1
	for {
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		pkt := make([]byte, int(hdr[1])<<16|int(hdr[2])<<8|int(hdr[3]))
		if _, err := io.ReadFull(c, pkt); err != nil {
			return
		}
		if len(pkt) < 64 || !bytes.Equal(pkt[:4], []byte("\xfeSMB")) {
			return
		}
		cmd := binary.LittleEndian.Uint16(pkt[12:])
		s.Commands = append(s.Commands, smbCommandNames[cmd])
		body := pkt[64:]
		status := uint32(smbStatusSuccess)
		var resp []byte
		sid := binary.LittleEndian.Uint64(pkt[40:])
		tid := binary.LittleEndian.Uint32(pkt[36:])
		switch cmd {
		case 0: // NEGOTIATE: answer SMB 2.1, signing enabled but not required
			resp = make([]byte, 65)
			binary.LittleEndian.PutUint16(resp[0:], 65)
			binary.LittleEndian.PutUint16(resp[2:], 1)
			binary.LittleEndian.PutUint16(resp[4:], 0x0210)
			copy(resp[8:24], "fake-smb-server!")
			binary.LittleEndian.PutUint32(resp[28:], 1<<16)
			binary.LittleEndian.PutUint32(resp[32:], 1<<16)
			binary.LittleEndian.PutUint32(resp[36:], 1<<16)
			binary.LittleEndian.PutUint16(resp[56:], 128)
		case 1: // SESSION_SETUP
			off := int(binary.LittleEndian.Uint16(body[12:]))
			l := int(binary.LittleEndian.Uint16(body[14:]))
			blob := pkt[off : off+l]
			i := bytes.Index(blob, []byte("NTLMSSP\x00"))
			if i < 0 || len(blob) < i+12 {
				return
			}
			ntlm := blob[i:]
			sid = sessionID
			var token []byte
			switch binary.LittleEndian.Uint32(ntlm[8:]) {
			case 1:
				status = smbStatusMoreProcessing
				token = spnegoResp(1, ntlmChallenge(binary.LittleEndian.Uint32(ntlm[12:])))
			case 3:
				if !f.verifyNTLMv2(ntlm, &s) {
					status = smbStatusLogonFailure
					break
				}
				token = spnegoResp(0, nil)
			default:
				return
			}
			if status == smbStatusLogonFailure {
				break
			}
			resp = make([]byte, 8)
			binary.LittleEndian.PutUint16(resp[0:], 9)
			binary.LittleEndian.PutUint16(resp[2:], smbSessionFlagIsGuest)
			binary.LittleEndian.PutUint16(resp[4:], 72)
			binary.LittleEndian.PutUint16(resp[6:], uint16(len(token)))
			resp = append(resp, token...)
		case 3: // TREE_CONNECT
			off := int(binary.LittleEndian.Uint16(body[4:]))
			l := int(binary.LittleEndian.Uint16(body[6:]))
			path := fromU16le(pkt[off : off+l])
			s.TreePaths = append(s.TreePaths, path)
			if !strings.EqualFold(path[strings.LastIndex(path, `\`)+1:], f.share) {
				status = smbStatusBadNetworkName
				break
			}
			tid = 1
			resp = make([]byte, 16)
			binary.LittleEndian.PutUint16(resp[0:], 16)
			resp[2] = 1 // disk
			binary.LittleEndian.PutUint32(resp[12:], 0x001f01ff)
		case 5: // CREATE
			off := int(binary.LittleEndian.Uint16(body[44:]))
			l := int(binary.LittleEndian.Uint16(body[46:]))
			name := fromU16le(pkt[off : off+l])
			content, ok := f.files[name]
			if !ok {
				status = smbStatusObjectNotFound
				break
			}
			id := nextFile
			nextFile++
			openFiles[id] = name
			resp = make([]byte, 89)
			binary.LittleEndian.PutUint16(resp[0:], 89)
			binary.LittleEndian.PutUint32(resp[4:], 1) // FILE_OPENED
			binary.LittleEndian.PutUint64(resp[40:], uint64(len(content)))
			binary.LittleEndian.PutUint64(resp[48:], uint64(len(content)))
			binary.LittleEndian.PutUint32(resp[56:], 0x80) // FILE_ATTRIBUTE_NORMAL
			binary.LittleEndian.PutUint64(resp[64:], id)
			binary.LittleEndian.PutUint64(resp[72:], id)
		case 8: // READ
			length := int(binary.LittleEndian.Uint32(body[4:]))
			offset := int(binary.LittleEndian.Uint64(body[8:]))
			content := f.files[openFiles[binary.LittleEndian.Uint64(body[16:])]]
			if offset >= len(content) {
				status = smbStatusEndOfFile
				break
			}
			data := content[offset:min(len(content), offset+length)]
			resp = make([]byte, 16)
			binary.LittleEndian.PutUint16(resp[0:], 17)
			resp[2] = 80
			binary.LittleEndian.PutUint32(resp[4:], uint32(len(data)))
			resp = append(resp, data...)
		case 6: // CLOSE
			delete(openFiles, binary.LittleEndian.Uint64(body[8:]))
			resp = make([]byte, 60)
			binary.LittleEndian.PutUint16(resp[0:], 60)
		case 2, 4: // LOGOFF, TREE_DISCONNECT
			resp = make([]byte, 4)
			binary.LittleEndian.PutUint16(resp[0:], 4)
		default:
			status = 0xC00000BB // STATUS_NOT_SUPPORTED
		}
		if status != smbStatusSuccess && status != smbStatusMoreProcessing {
			resp = make([]byte, 9) // ERROR response
			binary.LittleEndian.PutUint16(resp[0:], 9)
		}
		out := make([]byte, 64, 64+len(resp))
		copy(out, pkt[:64])
		binary.LittleEndian.PutUint16(out[6:], 0)
		binary.LittleEndian.PutUint32(out[8:], status)
		binary.LittleEndian.PutUint16(out[14:], 64)
		binary.LittleEndian.PutUint32(out[16:], 1) // SMB2_FLAGS_SERVER_TO_REDIR
		binary.LittleEndian.PutUint32(out[20:], 0)
		binary.LittleEndian.PutUint32(out[36:], tid)
		binary.LittleEndian.PutUint64(out[40:], sid)
		clear(out[48:64])
		out = append(out, resp...)
		frame := []byte{0, byte(len(out) >> 16), byte(len(out) >> 8), byte(len(out))}
		if _, err := c.Write(append(frame, out...)); err != nil {
			return
		}
	}
}

func (f *fakeSMB) last(t *testing.T) smbSession {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.sessions)
		var s smbSession
		if n > 0 {
			s = f.sessions[n-1]
		}
		f.mu.Unlock()
		if n > 0 {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no smb connection recorded")
	return smbSession{}
}

func startSMB(t *testing.T, f *fakeSMB) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return hostPort(t, ln.Addr().String())
}

func newSmbCheck(host string, port int, password string, files ...smbFile) *Smb {
	c := &Smb{Service: Service{Name: "test-smb", Target: host, Port: port, Timeout: 5, CredLists: []string{"users"}}, Share: "share", File: files}
	c.SetTaskCredentials([]TaskCredential{{Credlist: "users", Username: "tester", Password: password}})
	return c
}

func TestSmbRun_Loopback(t *testing.T) {
	f := &fakeSMB{user: "tester", password: "Passw0rd!", share: "share", files: map[string]string{"flag.txt": "hello tells\n"}}
	host, port := startSMB(t, f)
	flagHash, err := StringHash("hello tells\n")
	require.NoError(t, err)

	t.Run("login only", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!"))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
		assert.Equal(t, "smb login succeeded, creds tester:Passw0rd!", r.Debug)
	})
	t.Run("file regex", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Regex: "hello t.lls"}))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
		assert.Equal(t, "smb file flag.txt matched regex, creds tester:Passw0rd!", r.Debug)
	})
	t.Run("file hash", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Hash: flagHash}))
		assert.True(t, r.Status, r.Error+" "+r.Debug)
	})
	t.Run("file hash mismatch", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Hash: "00"}))
		assert.False(t, r.Status)
		assert.Equal(t, "file hash did not match", r.Error)
	})
	t.Run("regex not found", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Regex: "goodbye"}))
		assert.False(t, r.Status)
		assert.Equal(t, "couldn't find regex in file", r.Error)
	})
	t.Run("missing file", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "nope.txt"}))
		assert.False(t, r.Status)
		assert.Equal(t, "failed to open file", r.Error)
	})
	t.Run("wrong password", func(t *testing.T) {
		r := runCheck(t, newSmbCheck(host, port, "wrong"))
		assert.False(t, r.Status)
		assert.Equal(t, "smb login failed", r.Error)
	})
	t.Run("missing share", func(t *testing.T) {
		c := newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt"})
		c.Share = "other"
		r := runCheck(t, c)
		assert.False(t, r.Status)
		assert.Equal(t, "failed to mount share", r.Error)
	})
	t.Run("connection refused", func(t *testing.T) {
		r := runCheck(t, newSmbCheck("127.0.0.1", closedPort(t), "Passw0rd!"))
		assert.False(t, r.Status)
		assert.Equal(t, "smb connection failed", r.Error)
	})
}
