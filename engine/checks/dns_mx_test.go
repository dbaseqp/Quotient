package checks

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README documents MX as a supported record kind; a team DNS server that
// answers with exactly the configured mail exchanger must pass.
func TestDnsMxAnswerMatchesExpectedExchanger(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		if len(req.Question) == 1 && req.Question[0].Qtype == dns.TypeMX {
			rr, _ := dns.NewRR(req.Question[0].Name + " 60 IN MX 10 Mail.Team01.example.com.")
			m.Answer = append(m.Answer, rr)
		}
		_ = w.WriteMsg(m)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	for _, tc := range []struct {
		expected string
		pass     bool
	}{
		{"mail.team_.example.com", true},
		{"mail.team_.example.com.", true},
		{"other.team_.example.com", false},
	} {
		c := &Dns{Record: []DnsRecord{{Kind: "MX", Domain: "team_.example.com", Answer: []string{tc.expected}}}}
		require.NoError(t, c.Verify("box01", "127.0.0.1", 10, 5, 0, 0))
		c.Port = pc.LocalAddr().(*net.UDPAddr).Port

		results := make(chan Result, 1)
		c.Run(1, "01", 1, results)
		select {
		case r := <-results:
			assert.Equal(t, tc.pass, r.Status, "expected %q: error=%q debug=%q", tc.expected, r.Error, r.Debug)
		case <-time.After(10 * time.Second):
			t.Fatal("no result")
		}
	}
}

func TestDnsVerifyRejectsUnsupportedKind(t *testing.T) {
	c := &Dns{Record: []DnsRecord{{Kind: "AAAA", Domain: "team_.example.com", Answer: []string{"::1"}}}}
	err := c.Verify("box01", "127.0.0.1", 10, 5, 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported record kind")
}
