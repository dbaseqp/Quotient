package checks

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// realKEXINIT is a tellcap record of a real OpenSSH client's first KEXINIT
// (testdata/openssh_client_kexinit.jsonl, captured with default settings and
// an empty known_hosts).
type realKEXINIT struct {
	Source  string            `json:"source"`
	Banner  string            `json:"client_banner"`
	KexInit map[string]string `json:"kexinit"`
}

func loadRealKEXINITs(t *testing.T) map[string]realKEXINIT {
	t.Helper()
	f, err := os.Open("testdata/openssh_client_kexinit.jsonl")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	out := map[string]realKEXINIT{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r realKEXINIT
		require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
		out[r.Banner] = r
	}
	require.NoError(t, sc.Err())
	return out
}

const kexMarkers = "ext-info-c,kex-strict-c-v00@openssh.com"

// Every profile's banner and algorithm lists must be the pair a real client
// of that version sends.
func TestSSHProfiles_MatchRealClientCapture(t *testing.T) {
	captured := loadRealKEXINITs(t)
	require.NotEmpty(t, sshClientProfiles)
	for _, p := range sshClientProfiles {
		t.Run(p.banner, func(t *testing.T) {
			r, ok := captured[p.banner]
			require.True(t, ok, "no capture of a real client sending %q", p.banner)
			assert.Equal(t, r.KexInit["kex"], strings.Join(p.kex, ",")+","+kexMarkers)
			assert.Equal(t, r.KexInit["enc_c2s"], strings.Join(p.ciphers, ","))
			assert.Equal(t, r.KexInit["mac_c2s"], strings.Join(p.macs, ","))
			assert.Equal(t, r.KexInit["hostkey"], strings.Join(p.hostKeyAlgos, ","))
		})
	}
}

// readClientKEXINIT plays the server side up to the client's KEXINIT and
// returns the client banner and the eight name-lists.
func readClientKEXINIT(c net.Conn) (string, []string, error) {
	if _, err := fmt.Fprint(c, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20\r\n"); err != nil {
		return "", nil, err
	}
	r := bufio.NewReader(c)
	banner, err := r.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	pkt := make([]byte, binary.BigEndian.Uint32(hdr[:4])-1)
	if _, err := io.ReadFull(r, pkt); err != nil {
		return "", nil, err
	}
	if pkt[0] != 20 {
		return "", nil, fmt.Errorf("message %d is not KEXINIT", pkt[0])
	}
	p := pkt[17:]
	lists := make([]string, 8)
	for i := range lists {
		n := binary.BigEndian.Uint32(p)
		lists[i] = string(p[4 : 4+n])
		p = p[4+n:]
	}
	return strings.TrimRight(banner, "\r\n"), lists, nil
}

func withoutUnimplemented(algos []string) string {
	return strings.Join(slices.DeleteFunc(slices.Clone(algos), func(a string) bool {
		return slices.Contains(sshUnimplementedAlgos, a)
	}), ",")
}

// The KEXINIT on the wire is the profile's lists minus exactly
// sshUnimplementedAlgos. If x/crypto/ssh starts implementing one of them, or
// stops implementing another, this fails and the residual must be revisited.
func TestSSHProfiles_WireKEXINIT(t *testing.T) {
	for _, p := range sshClientProfiles {
		t.Run(p.banner, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = ln.Close() }()
			type got struct {
				banner string
				lists  []string
				err    error
			}
			done := make(chan got, 1)
			go func() {
				c, err := ln.Accept()
				if err != nil {
					done <- got{err: err}
					return
				}
				defer func() { _ = c.Close() }()
				b, l, err := readClientKEXINIT(c)
				done <- got{b, l, err}
			}()
			_, _ = ssh.Dial("tcp", ln.Addr().String(), p.clientConfig("u", nil, 5*time.Second))
			g := <-done
			require.NoError(t, g.err)
			assert.Equal(t, p.banner, g.banner)
			assert.Equal(t, withoutUnimplemented(p.kex)+","+kexMarkers, g.lists[0], "kex")
			assert.Equal(t, strings.Join(p.hostKeyAlgos, ","), g.lists[1], "host key algorithms")
			assert.Equal(t, withoutUnimplemented(p.ciphers), g.lists[2], "ciphers c2s")
			assert.Equal(t, withoutUnimplemented(p.ciphers), g.lists[3], "ciphers s2c")
			assert.Equal(t, withoutUnimplemented(p.macs), g.lists[4], "macs c2s")
			assert.Equal(t, withoutUnimplemented(p.macs), g.lists[5], "macs s2c")
			assert.Equal(t, "none", g.lists[6], "compression c2s")
		})
	}
}

func TestSSHProfileFor_StablePerTarget(t *testing.T) {
	for _, target := range []string{"10.100.101.12", "10.100.102.12", "10.100.127.13"} {
		first := sshProfileFor(target).banner
		for range 20 {
			assert.Equal(t, first, sshProfileFor(target).banner)
		}
	}
}

// sshTestServer is an in-process x/crypto/ssh server with a fake shell that
// answers each line with "out:<line>". It records what each client did.
type sshTestServer struct {
	port     int
	mu       sync.Mutex
	banners  []string
	authOK   int
	ptyReqs  [][]byte
	globals  []string
	lines    []string
	password string
}

func (s *sshTestServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.banners)
}

// startSSHTestServer starts a server. legacy restricts it to algorithms
// x/crypto/ssh's old defaults reach only with the 3des-cbc append:
// diffie-hellman-group14-sha1, 3des-cbc, hmac-sha1 and an ssh-rsa host key.
func startSSHTestServer(t *testing.T, legacy bool) *sshTestServer {
	t.Helper()
	s := &sshTestServer{password: "s3cret"}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == s.password {
				s.mu.Lock()
				s.authOK++
				s.mu.Unlock()
				return nil, nil
			}
			return nil, fmt.Errorf("rejected")
		},
	}
	if legacy {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		signer, err := ssh.NewSignerFromKey(key)
		require.NoError(t, err)
		signer, err = ssh.NewSignerWithAlgorithms(signer.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
		require.NoError(t, err)
		cfg.AddHostKey(signer)
		cfg.KeyExchanges = []string{"diffie-hellman-group14-sha1"}
		cfg.Ciphers = []string{"3des-cbc"}
		cfg.MACs = []string{"hmac-sha1"}
	} else {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		signer, err := ssh.NewSignerFromKey(priv)
		require.NoError(t, err)
		cfg.AddHostKey(signer)
	}
	cfg.ServerVersion = "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			nConn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(nConn, cfg)
		}
	}()
	return s
}

// bannerConn records the client's version line as the server reads it.
type bannerConn struct {
	net.Conn
	s    *sshTestServer
	buf  []byte
	done bool
}

func (c *bannerConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.done {
		c.buf = append(c.buf, p[:n]...)
		if i := strings.IndexByte(string(c.buf), '\n'); i >= 0 {
			c.done = true
			c.s.mu.Lock()
			c.s.banners = append(c.s.banners, strings.TrimRight(string(c.buf[:i]), "\r"))
			c.s.mu.Unlock()
		}
	}
	return n, err
}

func (s *sshTestServer) serve(nConn net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = nConn.Close() }()
	conn, chans, reqs, err := ssh.NewServerConn(&bannerConn{Conn: nConn, s: s}, cfg)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	go func() {
		for r := range reqs {
			s.mu.Lock()
			s.globals = append(s.globals, r.Type)
			s.mu.Unlock()
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		shell := make(chan struct{})
		go func() {
			for r := range creqs {
				switch r.Type {
				case "pty-req":
					s.mu.Lock()
					s.ptyReqs = append(s.ptyReqs, r.Payload)
					s.mu.Unlock()
				case "shell":
					close(shell)
				}
				if r.WantReply {
					_ = r.Reply(true, nil)
				}
			}
		}()
		go func() {
			defer func() { _ = ch.Close() }()
			<-shell
			sc := bufio.NewScanner(ch)
			for sc.Scan() {
				line := strings.TrimRight(sc.Text(), "\r")
				s.mu.Lock()
				s.lines = append(s.lines, line)
				s.mu.Unlock()
				_, _ = fmt.Fprintf(ch, "out:%s\r\n", line)
			}
		}()
	}
}

func runSshCheck(t *testing.T, port int, password string, badAttempts int) Result {
	t.Helper()
	c := &Ssh{
		Service: Service{
			Name:            "test-ssh",
			Target:          "127.0.0.1",
			Port:            port,
			Timeout:         4,
			TaskCredentials: []TaskCredential{{Username: "jdoe", Password: password}},
		},
		BadAttempts: badAttempts,
		Command:     []commandData{{Command: "hostname", Contains: true, Output: "out:hostname"}},
	}
	res := make(chan Result, 1)
	c.Run(1, "01", 1, res)
	select {
	case r := <-res:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("ssh check timed out")
		return Result{}
	}
}

func TestSshRun_ProfileAgainstModernServer(t *testing.T) {
	s := startSSHTestServer(t, false)
	r := runSshCheck(t, s.port, "s3cret", 0)
	require.True(t, r.Status, "error: %s debug: %s", r.Error, r.Debug)

	want := sshProfileFor("127.0.0.1").banner
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Equal(t, []string{want}, s.banners, "one connection, presenting the profile's banner")
	assert.Equal(t, []string{"hostname"}, s.lines, "command typed into the shell once")
	assert.Contains(t, s.globals, "no-more-sessions@openssh.com")
	require.Len(t, s.ptyReqs, 1)
	var pty struct {
		Term          string
		Columns, Rows uint32
		Width, Height uint32
		Modes         string
	}
	require.NoError(t, ssh.Unmarshal(s.ptyReqs[0], &pty))
	assert.Equal(t, "xterm", pty.Term)
	assert.Equal(t, [4]uint32{80, 40, 0, 0}, [4]uint32{pty.Columns, pty.Rows, pty.Width, pty.Height})
	assert.Len(t, pty.Modes, len(opensshPtyModes)*5+1)
	modes := map[byte]uint32{}
	for m := []byte(pty.Modes); len(m) >= 5; m = m[5:] {
		modes[m[0]] = binary.BigEndian.Uint32(m[1:5])
	}
	assert.Equal(t, uint32(0), modes[ssh.ECHO], "echo stays off")
}

func TestSshRun_WrongPasswordIsNotRetried(t *testing.T) {
	s := startSSHTestServer(t, false)
	r := runSshCheck(t, s.port, "wrong", 0)
	assert.False(t, r.Status)
	assert.Contains(t, r.Error, "error logging in to ssh server")
	assert.Equal(t, 1, s.connections(), "an authentication failure must not trigger the legacy retry")
}

func TestSshRun_LegacyOnlyServerStillScores(t *testing.T) {
	s := startSSHTestServer(t, true)
	r := runSshCheck(t, s.port, "s3cret", 0)
	require.True(t, r.Status, "error: %s debug: %s", r.Error, r.Debug)
	s.mu.Lock()
	defer s.mu.Unlock()
	want := sshProfileFor("127.0.0.1").banner
	assert.Equal(t, []string{want, want}, s.banners, "profile attempt, then legacy retry with the same banner")
	assert.Equal(t, 1, s.authOK)
	assert.Equal(t, []string{"hostname"}, s.lines)
}

func TestSshRun_LegacyOnlyServerWrongPasswordFails(t *testing.T) {
	s := startSSHTestServer(t, true)
	r := runSshCheck(t, s.port, "wrong", 0)
	assert.False(t, r.Status)
	assert.Contains(t, r.Error, "error logging in to ssh server")
	assert.Equal(t, 2, s.connections())
}

func TestSshRun_BadAttemptsShareTheProfile(t *testing.T) {
	s := startSSHTestServer(t, false)
	r := runSshCheck(t, s.port, "s3cret", 2)
	require.True(t, r.Status, "error: %s debug: %s", r.Error, r.Debug)
	want := sshProfileFor("127.0.0.1").banner
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Equal(t, []string{want, want, want}, s.banners)
	assert.Equal(t, 1, s.authOK)
}
