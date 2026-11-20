package checks

import (
	"errors"
	"net"
	"os"
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
	pc, host, port := listenDNS(t)
	results := startNetCheck(newDnsCheck(host, port, "A", "www.team_.acme.local", "192.0.2.10"))
	a, client := readDNS(t, pc)
	aaaa, client2 := readDNS(t, pc)
	require.Len(t, a.Question, 1)
	require.Len(t, aaaa.Question, 1)
	assert.Equal(t, dns.TypeA, a.Question[0].Qtype, "the A question goes first")
	assert.Equal(t, dns.TypeAAAA, aaaa.Question[0].Qtype)
	assert.Equal(t, a.Question[0].Name, aaaa.Question[0].Name)
	assert.Equal(t, client.String(), client2.String(), "both questions go out on one socket, like getaddrinfo")
	reply := new(dns.Msg).SetReply(a)
	reply.Answer = append(reply.Answer, dnsA(a.Question[0].Name, "192.0.2.10"))
	writeDNS(t, pc, reply, client)
	writeDNS(t, pc, new(dns.Msg).SetReply(aaaa), client)
	r := awaitResult(t, results)
	require.True(t, r.Status, r.Error+" "+r.Debug)

	// An MX lookup sends only its own question. The check has read its reply by the time it
	// returns, so anything else it sent is already queued ahead of a marker sent afterwards.
	pc, mh, mp := listenDNS(t)
	results = startNetCheck(newDnsCheck(mh, mp, "MX", "acme.local", "192.0.2.10"))
	mq, client := readDNS(t, pc)
	require.Len(t, mq.Question, 1)
	assert.Equal(t, dns.TypeMX, mq.Question[0].Qtype)
	writeDNS(t, pc, new(dns.Msg).SetReply(mq), client)
	_ = awaitResult(t, results)

	marker, err := net.Dial("udp", pc.LocalAddr().String())
	require.NoError(t, err)
	defer func() { _ = marker.Close() }()
	b, err := new(dns.Msg).SetQuestion("marker.", dns.TypeTXT).Pack()
	require.NoError(t, err)
	_, err = marker.Write(b)
	require.NoError(t, err)
	next, _ := readDNS(t, pc)
	assert.Equal(t, "marker.", next.Question[0].Name, "no question after the MX one")
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
	r := runNetCheck(t, &Tcp{Service: Service{Name: "test-tcp", Target: host, Port: port, Timeout: 3}})
	require.True(t, r.Status, r.Debug)
	assertClosedPromptly(t, flows)
}

func TestRdpRun_ClosesConnection(t *testing.T) {
	host, port, flows := startTCPSink(t, 2*time.Second)
	r := runNetCheck(t, &Rdp{Service: Service{Name: "test-rdp", Target: host, Port: port, Timeout: 3}})
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
			r := runNetCheck(t, newLdapCheck(host, port, "acme.local", "bugs.bunny", tc.password))
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

	r := runNetCheck(t, newSmbCheck(host, port, "Passw0rd!", smbFile{Name: "flag.txt", Regex: "hello"}))
	require.True(t, r.Status, r.Error+" "+r.Debug)
	waitFor(t, func() bool { return count() >= 1 })
	s := f.last(t)

	assert.Equal(t, []string{`\\127.0.0.1\share`}, s.TreePaths, "no :port in the tree-connect path")
	assert.Contains(t, workstationPool("", "01", nil), s.Workstation, "one of team 01's workstation names")
	assert.NotContains(t, s.AvPairs, uint16(9), "no MsvAvTargetName: the check names no SPN")
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
	defer func() { _ = ln.Close() }()

	c := &Ping{Service: Service{Name: "test-ping", Target: "127.0.0.1", Timeout: 10}, Count: 2}
	r := runNetCheck(t, c)
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
