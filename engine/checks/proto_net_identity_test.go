package checks

import (
	"errors"
	"net"
	"os"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wire-identity tests for the network checks: what a blue team sees on the wire or in server logs.

// waitFor polls cond until it is true or 3 s pass. The checks report their result before their
// deferred close/unbind/logoff runs, so the servers record a connection slightly later.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 3s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDnsRun_StubQueryPair(t *testing.T) {
	f := &fakeDNS{answers: map[string]string{"www.team01.acme.local.": "192.0.2.10"}}
	host, port := startDNS(t, f)

	r := runCheck(t, newDnsCheck(host, port, "A", "www.team_.acme.local", "192.0.2.10"))
	require.True(t, r.Status, r.Error+" "+r.Debug)
	waitFor(t, func() bool { return len(f.snapshot()) >= 2 })
	q := f.snapshot()
	require.Len(t, q, 2)
	// The server handles packets concurrently, so its log order is not the send order.
	assert.ElementsMatch(t, []uint16{dns.TypeA, dns.TypeAAAA}, []uint16{q[0].Qtype, q[1].Qtype})
	assert.Equal(t, q[0].Name, q[1].Name)
	assert.Equal(t, q[0].Peer, q[1].Peer, "both questions go out on one socket, like getaddrinfo")

	mx := &fakeDNS{answers: map[string]string{}}
	mh, mp := startDNS(t, mx)
	_ = runCheck(t, newDnsCheck(mh, mp, "MX", "acme.local", "192.0.2.10"))
	waitFor(t, func() bool { return len(mx.snapshot()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	require.Len(t, mx.snapshot(), 1)
	assert.Equal(t, dns.TypeMX, mx.snapshot()[0].Qtype)
}

func assertClosedPromptly(t *testing.T, flows <-chan tcpFlow) {
	t.Helper()
	select {
	case f := <-flows:
		assert.Equal(t, "fin", f.Closed)
		assert.Zero(t, f.ClientBytes)
		assert.Less(t, f.CloseAfter, time.Second)
	case <-time.After(4 * time.Second):
		t.Fatal("no flow recorded")
	}
}

func TestTcpRun_ClosesConnection(t *testing.T) {
	host, port, flows := startTCPSink(t, 2*time.Second)
	r := runCheck(t, &Tcp{Service: Service{Name: "test-tcp", Target: host, Port: port, Timeout: 3}})
	require.True(t, r.Status, r.Debug)
	assertClosedPromptly(t, flows)
}

func TestRdpRun_ClosesConnection(t *testing.T) {
	host, port, flows := startTCPSink(t, 2*time.Second)
	r := runCheck(t, &Rdp{Service: Service{Name: "test-rdp", Target: host, Port: port, Timeout: 3}})
	require.True(t, r.Status, r.Debug)
	assertClosedPromptly(t, flows)
}

func TestLdapRun_Unbinds(t *testing.T) {
	f := &fakeLDAP{user: "bugs.bunny@acme.local", password: "Carrots1!"}
	host, port := startLDAP(t, f)
	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.ops) }

	for _, tc := range []struct {
		name, password string
		status         bool
	}{{"bind accepted", "Carrots1!", true}, {"bind rejected", "wrong", false}} {
		t.Run(tc.name, func(t *testing.T) {
			before := count()
			r := runCheck(t, newLdapCheck(host, port, "acme.local", "bugs.bunny", tc.password))
			assert.Equal(t, tc.status, r.Status, r.Error)
			waitFor(t, func() bool { return count() > before })
			assert.Equal(t, "bind,unbind|fin", f.last(t))
		})
	}
}

func TestSmbRun_ClientIdentity(t *testing.T) {
	f := &fakeSMB{user: "tester", password: "Passw0rd!", share: "share", files: map[string]string{"flag.txt": "hello tells\n"}}
	host, port := startSMB(t, f)
	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sessions) }

	r := runCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Regex: "hello"}))
	require.True(t, r.Status, r.Error+" "+r.Debug)
	waitFor(t, func() bool { return count() >= 1 })
	s := f.last(t)

	assert.Equal(t, []string{`\\127.0.0.1\share`}, s.TreePaths, "no :port in the tree-connect path")
	assert.Regexp(t, regexp.MustCompile(`^DESKTOP-[A-Z0-9]{7}$`), s.Workstation)
	assert.Equal(t, workstationName(1, "test-smb"), s.Workstation, "stable per team and check")
	assert.Equal(t, "cifs/127.0.0.1", fromU16le(s.AvPairs[9]), "MsvAvTargetName")
	assert.Equal(t, "tester", s.User)

	t.Run("unc share passes through", func(t *testing.T) {
		assert.Equal(t, `\\files\docs`, uncShare("10.0.0.5", `\\files\docs`))
		assert.Equal(t, `\\10.0.0.5\docs`, uncShare("10.0.0.5", "docs"))
	})
}

// TestPingRun_PayloadSize needs a raw ICMP socket (root or CAP_NET_RAW), as the ping check itself does.
func TestPingRun_PayloadSize(t *testing.T) {
	ln, err := net.ListenPacket("ip4:icmp", "127.0.0.1")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrPermission) {
			t.Skip("needs CAP_NET_RAW")
		}
		require.NoError(t, err)
	}
	defer ln.Close()

	c := &Ping{Service: Service{Name: "test-ping", Target: "127.0.0.1", Timeout: 10}, Count: 2}
	r := runCheck(t, c)
	require.True(t, r.Status, r.Error+" "+r.Debug)

	buf := make([]byte, 1500)
	var sizes []int
	_ = ln.SetReadDeadline(time.Now().Add(time.Second))
	for {
		n, _, err := ln.ReadFrom(buf)
		if err != nil {
			break
		}
		if n >= 8 && buf[0] == 8 { // echo request; ReadFrom strips the IP header
			sizes = append(sizes, n-8)
		}
	}
	require.NotEmpty(t, sizes)
	for _, s := range sizes {
		assert.Equal(t, 56, s, "ICMP data bytes, as iputils ping sends")
	}
}
