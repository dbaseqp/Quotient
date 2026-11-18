package checks

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- NTLMv2 uses HMAC-MD5; the fake WinRM server has to verify it
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/md4" //nolint:staticcheck // NTLM hashes are MD4 by definition
)

// These loopback tests pin the pass/fail result of the smtp, pop3, ftp and winrm checks against
// scripted servers, so that changes to what the checks send on the wire cannot change scoring.

// lineState is the per-connection state of a scripted line-protocol server.
type lineState struct {
	data   bool         // smtp: inside DATA
	quit   bool         // close after this reply
	dataLn net.Listener // ftp: passive data listener
	msg    []string     // smtp: message lines received in DATA
}

// lineServer is a loopback server for line-based protocols that records every client line.
type lineServer struct {
	port int
	mu   sync.Mutex
	conn [][]string // client lines per connection
	msgs [][]string // smtp messages
}

func (s *lineServer) sessions() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.conn...)
}

func (s *lineServer) messages() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.msgs...)
}

// startLineServer sends greeting to each client, then calls reply for every line it reads.
func startLineServer(t *testing.T, greeting string, reply func(st *lineState, w io.Writer, line string)) *lineServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &lineServer{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			idx := len(s.conn)
			s.conn = append(s.conn, nil)
			s.mu.Unlock()
			go func(c net.Conn, idx int) {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.WriteString(c, greeting)
				r := bufio.NewReader(c)
				st := &lineState{}
				for !st.quit {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					s.mu.Lock()
					s.conn[idx] = append(s.conn[idx], line)
					s.mu.Unlock()
					wasData := st.data
					reply(st, c, line)
					if wasData && !st.data {
						s.mu.Lock()
						s.msgs = append(s.msgs, st.msg)
						s.mu.Unlock()
						st.msg = nil
					}
				}
			}(c, idx)
		}
	}()
	return s
}

func testService(port int, creds ...TaskCredential) Service {
	svc := Service{Name: "box-test", Target: "127.0.0.1", Port: port, Timeout: 5, Points: 5}
	if len(creds) > 0 {
		svc.CredLists = []string{"users"}
		svc.TaskCredentials = creds
	}
	return svc
}

var loopbackCred = TaskCredential{Credlist: "users", Username: "bugs.bunny", Password: "Carrots1!"}

func runCheck(t *testing.T, r Runner) Result {
	t.Helper()
	ch := make(chan Result, 1)
	r.Run(1, "01", 1, ch)
	select {
	case res := <-ch:
		return res
	case <-time.After(15 * time.Second):
		t.Fatal("check did not report")
	}
	return Result{}
}

// waitSession returns connection idx's client lines once the server has seen last (the check
// reports before its deferred QUIT/DATA terminator reach the server).
func waitSession(t *testing.T, s *lineServer, idx int, last string) []string {
	t.Helper()
	var lines []string
	require.Eventually(t, func() bool {
		sessions := s.sessions()
		if len(sessions) <= idx {
			return false
		}
		lines = sessions[idx]
		return slices.Contains(lines, last)
	}, 5*time.Second, 20*time.Millisecond)
	return lines
}

// ---------------------------------------------------------------- smtp

type smtpBehaviour struct {
	noEhlo     bool // answer EHLO with 502 so the client falls back to HELO
	rejectAuth bool
	rejectRcpt bool
}

func startSMTP(t *testing.T, b smtpBehaviour) *lineServer {
	return startLineServer(t, "220 mail.acme.local ESMTP Postfix\r\n", func(st *lineState, w io.Writer, line string) {
		if st.data {
			if line == "." {
				st.data = false
				_, _ = io.WriteString(w, "250 2.0.0 Ok: queued\r\n")
			} else {
				st.msg = append(st.msg, line)
			}
			return
		}
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch {
		case verb == "EHLO" && b.noEhlo:
			_, _ = io.WriteString(w, "502 5.5.2 Error: command not recognized\r\n")
		case verb == "EHLO":
			_, _ = io.WriteString(w, "250-mail.acme.local\r\n250-PIPELINING\r\n250-SIZE 10240000\r\n250-AUTH PLAIN LOGIN\r\n250-8BITMIME\r\n250 DSN\r\n")
		case verb == "HELO":
			_, _ = io.WriteString(w, "250 mail.acme.local\r\n")
		case verb == "AUTH" && b.rejectAuth:
			_, _ = io.WriteString(w, "535 5.7.8 Error: authentication failed\r\n")
		case verb == "AUTH":
			_, _ = io.WriteString(w, "235 2.7.0 Authentication successful\r\n")
		case verb == "RCPT" && b.rejectRcpt:
			_, _ = io.WriteString(w, "550 5.1.1 Recipient address rejected\r\n")
		case verb == "MAIL" || verb == "RCPT" || verb == "RSET" || verb == "NOOP":
			_, _ = io.WriteString(w, "250 2.1.0 Ok\r\n")
		case verb == "DATA":
			st.data = true
			_, _ = io.WriteString(w, "354 End data with <CR><LF>.<CR><LF>\r\n")
		case verb == "QUIT":
			st.quit = true
			_, _ = io.WriteString(w, "221 2.0.0 Bye\r\n")
		default:
			_, _ = io.WriteString(w, "502 5.5.2 Error: command not recognized\r\n")
		}
	})
}

func TestSmtpRun_Loopback(t *testing.T) {
	tests := []struct {
		name   string
		b      smtpBehaviour
		pass   bool
		errMsg string
	}{
		{name: "accepted", pass: true},
		{name: "helo fallback", b: smtpBehaviour{noEhlo: true}, pass: true},
		{name: "auth rejected", b: smtpBehaviour{rejectAuth: true}, errMsg: "login failed"},
		{name: "recipient rejected", b: smtpBehaviour{rejectRcpt: true}, errMsg: "setting receiver failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startSMTP(t, tt.b)
			res := runCheck(t, &Smtp{Service: testService(srv.port, loopbackCred), Domain: "@acme.local"})
			assert.Equal(t, tt.pass, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
			if tt.errMsg != "" {
				assert.Contains(t, res.Error, tt.errMsg)
			}
		})
	}
}

// ---------------------------------------------------------------- pop3

type pop3Behaviour struct {
	rejectPass bool
	noCapa     bool // answer CAPA with -ERR, like a pre-RFC 2449 server
}

func startPOP3(t *testing.T, b pop3Behaviour) *lineServer {
	return startLineServer(t, "+OK Dovecot ready.\r\n", func(st *lineState, w io.Writer, line string) {
		switch strings.ToUpper(strings.SplitN(line, " ", 2)[0]) {
		case "CAPA":
			if b.noCapa {
				_, _ = io.WriteString(w, "-ERR unknown command\r\n")
			} else {
				_, _ = io.WriteString(w, "+OK\r\nCAPA\r\nTOP\r\nUIDL\r\nRESP-CODES\r\nUSER\r\nSASL PLAIN\r\n.\r\n")
			}
		case "PASS":
			if b.rejectPass {
				_, _ = io.WriteString(w, "-ERR [AUTH] Authentication failed.\r\n")
			} else {
				_, _ = io.WriteString(w, "+OK Logged in.\r\n")
			}
		case "STAT":
			_, _ = io.WriteString(w, "+OK 1 120\r\n")
		case "QUIT":
			st.quit = true
			_, _ = io.WriteString(w, "+OK Logging out.\r\n")
		default:
			_, _ = io.WriteString(w, "+OK\r\n")
		}
	})
}

func TestPop3Run_Loopback(t *testing.T) {
	tests := []struct {
		name   string
		b      pop3Behaviour
		creds  bool
		pass   bool
		errMsg string
	}{
		{name: "login and stat", creds: true, pass: true},
		{name: "server without CAPA", b: pop3Behaviour{noCapa: true}, creds: true, pass: true},
		{name: "password rejected", b: pop3Behaviour{rejectPass: true}, creds: true, errMsg: "login failed"},
		{name: "anonymous connect", pass: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startPOP3(t, tt.b)
			svc := testService(srv.port)
			if tt.creds {
				svc = testService(srv.port, loopbackCred)
			}
			res := runCheck(t, &Pop3{Service: svc})
			assert.Equal(t, tt.pass, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
			if tt.errMsg != "" {
				assert.Contains(t, res.Error, tt.errMsg)
			}
		})
	}
}

// ---------------------------------------------------------------- ftp

const ftpFileContent = "flag{acme-anvil}\n"

func startFTP(t *testing.T, rejectLogin bool) *lineServer {
	return startLineServer(t, "220 (vsFTPd 3.0.5)\r\n", func(st *lineState, w io.Writer, line string) {
		switch strings.ToUpper(strings.SplitN(line, " ", 2)[0]) {
		case "USER":
			_, _ = io.WriteString(w, "331 Please specify the password.\r\n")
		case "PASS":
			if rejectLogin {
				_, _ = io.WriteString(w, "530 Login incorrect.\r\n")
			} else {
				_, _ = io.WriteString(w, "230 Login successful.\r\n")
			}
		case "FEAT":
			_, _ = io.WriteString(w, "211-Features:\r\n EPSV\r\n PASV\r\n SIZE\r\n UTF8\r\n211 End\r\n")
		case "EPSV":
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_, _ = io.WriteString(w, "425 Cannot open data connection.\r\n")
				return
			}
			st.dataLn = ln
			_, _ = fmt.Fprintf(w, "229 Entering Extended Passive Mode (|||%d|)\r\n", ln.Addr().(*net.TCPAddr).Port)
		case "RETR":
			if st.dataLn == nil {
				_, _ = io.WriteString(w, "425 Use PASV or EPSV first.\r\n")
				return
			}
			_, _ = io.WriteString(w, "150 Opening BINARY mode data connection.\r\n")
			if d, err := st.dataLn.Accept(); err == nil {
				_, _ = io.WriteString(d, ftpFileContent)
				_ = d.Close()
			}
			_ = st.dataLn.Close()
			st.dataLn = nil
			_, _ = io.WriteString(w, "226 Transfer complete.\r\n")
		case "QUIT":
			st.quit = true
			_, _ = io.WriteString(w, "221 Goodbye.\r\n")
		default:
			_, _ = io.WriteString(w, "200 OK\r\n")
		}
	})
}

func TestFtpRun_Loopback(t *testing.T) {
	tests := []struct {
		name        string
		creds       bool
		rejectLogin bool
		regex       string
		pass        bool
		errMsg      string
	}{
		{name: "anonymous, file matches", regex: `flag\{`, pass: true},
		{name: "credentials, file matches", creds: true, regex: `acme-anvil`, pass: true},
		{name: "file does not match", regex: `nope`, errMsg: "couldn't find regex"},
		{name: "login rejected", rejectLogin: true, regex: `flag`, errMsg: "ftp login failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startFTP(t, tt.rejectLogin)
			svc := testService(srv.port)
			if tt.creds {
				svc = testService(srv.port, loopbackCred)
			}
			res := runCheck(t, &Ftp{Service: svc, File: []FtpFile{{Name: "flag.txt", Regex: tt.regex}}})
			assert.Equal(t, tt.pass, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
			if tt.errMsg != "" {
				assert.Contains(t, res.Error, tt.errMsg)
			}
		})
	}
}

// ---------------------------------------------------------------- winrm

// fakeWinRM is a loopback WinRM endpoint: connection-based NTLM (anonymous probe → 401 Negotiate,
// type1 → challenge, type3 verified with NTLMv2), then a minimal WinRS shell that returns stdout.
type fakeWinRM struct {
	srv      *httptest.Server
	port     int
	password string
	stdout   string
	mu       sync.Mutex
	authed   map[string]bool
	requests []http.Header
}

var ntlmServerChallenge = []byte{1, 2, 3, 4, 5, 6, 7, 8}

func startWinRM(t *testing.T, password, stdout string) *fakeWinRM {
	t.Helper()
	f := &fakeWinRM{password: password, stdout: stdout, authed: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.port = f.srv.Listener.Addr().(*net.TCPAddr).Port
	return f
}

func (f *fakeWinRM) headers() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.requests...)
}

func (f *fakeWinRM) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, r.Header.Clone())
	authed := f.authed[r.RemoteAddr]
	f.mu.Unlock()

	if !authed {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		msg, err := base64.StdEncoding.DecodeString(token)
		if scheme != "Negotiate" && scheme != "NTLM" || err != nil || len(msg) < 12 {
			w.Header().Add("WWW-Authenticate", "Negotiate")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch binary.LittleEndian.Uint32(msg[8:]) {
		case 1:
			w.Header().Set("WWW-Authenticate", scheme+" "+base64.StdEncoding.EncodeToString(ntlmChallenge(binary.LittleEndian.Uint32(msg[12:]))))
			w.WriteHeader(http.StatusUnauthorized)
			return
		case 3:
			if !ntlmV2Valid(msg, f.password) {
				w.Header().Add("WWW-Authenticate", "Negotiate")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.mu.Lock()
			f.authed[r.RemoteAddr] = true
			f.mu.Unlock()
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
	_, _ = io.WriteString(w, winrsReply(string(body), f.stdout))
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

// ntlmChallenge builds a CHALLENGE_MESSAGE that echoes the client's flags, as Windows does.
func ntlmChallenge(clientFlags uint32) []byte {
	av := func(id uint16, v []byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, id)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(v))) // #nosec G115 -- short test values
		return append(b, v...)
	}
	ts := binary.LittleEndian.AppendUint64(nil, uint64(time.Now().UnixNano()/100)+116444736000000000) // #nosec G115
	ti := append(append(append(av(2, utf16le("ACME")), av(1, utf16le("WIN-SRV01"))...), av(7, ts)...), 0, 0, 0, 0)
	target := utf16le("ACME")
	const hdr = 56
	m := []byte("NTLMSSP\x00")
	m = binary.LittleEndian.AppendUint32(m, 2)
	m = binary.LittleEndian.AppendUint16(m, uint16(len(target))) // #nosec G115
	m = binary.LittleEndian.AppendUint16(m, uint16(len(target))) // #nosec G115
	m = binary.LittleEndian.AppendUint32(m, hdr)
	m = binary.LittleEndian.AppendUint32(m, (0xE2898215&clientFlags)|0x00810004)
	m = append(m, ntlmServerChallenge...)
	m = append(m, make([]byte, 8)...)
	m = binary.LittleEndian.AppendUint16(m, uint16(len(ti)))         // #nosec G115
	m = binary.LittleEndian.AppendUint16(m, uint16(len(ti)))         // #nosec G115
	m = binary.LittleEndian.AppendUint32(m, uint32(hdr+len(target))) // #nosec G115
	m = append(m, 10, 0, 0x7c, 0x4f, 0, 0, 0, 15)
	return append(append(m, target...), ti...)
}

// ntlmV2Valid checks the NTProofStr of an AUTHENTICATE_MESSAGE against password.
func ntlmV2Valid(m []byte, password string) bool {
	field := func(off int) []byte {
		if len(m) < off+8 {
			return nil
		}
		l := int(binary.LittleEndian.Uint16(m[off:]))
		o := int(binary.LittleEndian.Uint32(m[off+4:]))
		if o+l > len(m) {
			return nil
		}
		return m[o : o+l]
	}
	nt, domain, user := field(20), field(28), field(36)
	if len(nt) < 32 {
		return false
	}
	dec := func(b []byte) string {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[2*i:])
		}
		return string(utf16.Decode(u))
	}
	h := md4.New()
	_, _ = h.Write(utf16le(password))
	k := hmac.New(md5.New, h.Sum(nil))
	_, _ = k.Write(utf16le(strings.ToUpper(dec(user)) + dec(domain)))
	p := hmac.New(md5.New, k.Sum(nil))
	_, _ = p.Write(ntlmServerChallenge)
	_, _ = p.Write(nt[16:])
	return hmac.Equal(p.Sum(nil), nt[:16])
}

func winrsReply(body, stdout string) string {
	const env = `<s:Envelope xml:lang="en-US" xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:x="http://schemas.xmlsoap.org/ws/2004/09/transfer" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Header><a:Action>%s</a:Action></s:Header><s:Body>%s</s:Body></s:Envelope>`
	const shell = "http://schemas.microsoft.com/wbem/wsman/1/windows/shell/"
	const cmd = "1A6DEE6B-EC68-4DD6-87E9-030C0048ECC4"
	switch {
	case strings.Contains(body, "transfer/Create"):
		return fmt.Sprintf(env, "http://schemas.xmlsoap.org/ws/2004/09/transfer/CreateResponse",
			`<x:ResourceCreated><a:ReferenceParameters><w:SelectorSet><w:Selector Name="ShellId">67A74734-DD32-4F10-89DE-49A060483810</w:Selector></w:SelectorSet></a:ReferenceParameters></x:ResourceCreated>`)
	case strings.Contains(body, "shell/Command"):
		return fmt.Sprintf(env, shell+"CommandResponse", "<rsp:CommandResponse><rsp:CommandId>"+cmd+"</rsp:CommandId></rsp:CommandResponse>")
	case strings.Contains(body, "shell/Receive"):
		out := base64.StdEncoding.EncodeToString([]byte(stdout))
		return fmt.Sprintf(env, shell+"ReceiveResponse", `<rsp:ReceiveResponse><rsp:Stream Name="stdout" CommandId="`+cmd+`">`+out+`</rsp:Stream>`+
			`<rsp:Stream Name="stdout" CommandId="`+cmd+`" End="true"></rsp:Stream><rsp:Stream Name="stderr" CommandId="`+cmd+`" End="true"></rsp:Stream>`+
			`<rsp:CommandState CommandId="`+cmd+`" State="`+shell+`CommandState/Done"><rsp:ExitCode>0</rsp:ExitCode></rsp:CommandState></rsp:ReceiveResponse>`)
	case strings.Contains(body, "shell/Signal"):
		return fmt.Sprintf(env, shell+"SignalResponse", "<rsp:SignalResponse/>")
	default:
		return fmt.Sprintf(env, "http://schemas.xmlsoap.org/ws/2004/09/transfer/DeleteResponse", "")
	}
}

func TestWinRMRun_Loopback(t *testing.T) {
	tests := []struct {
		name     string
		password string
		commands []winCommandData
		pass     bool
		errMsg   string
	}{
		{name: "connectivity test", password: "Carrots1!", pass: true},
		{name: "command output matches", password: "Carrots1!", commands: []winCommandData{{Command: "hostname", Output: "WIN-SRV01"}}, pass: true},
		{name: "command output regex matches", password: "Carrots1!", commands: []winCommandData{{Command: "hostname", Output: `^WIN-`, UseRegex: true}}, pass: true},
		{name: "command output differs", password: "Carrots1!", commands: []winCommandData{{Command: "hostname", Output: "DC01"}}, errMsg: "didn't match string"},
		{name: "wrong password", password: "something else", errMsg: "connection test failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startWinRM(t, tt.password, "WIN-SRV01\r\n")
			res := runCheck(t, &WinRM{Service: testService(srv.port, loopbackCred), Command: tt.commands})
			assert.Equal(t, tt.pass, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
			if tt.errMsg != "" {
				assert.Contains(t, res.Error, tt.errMsg)
			}
		})
	}
}
