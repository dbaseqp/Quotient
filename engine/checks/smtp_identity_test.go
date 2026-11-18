package checks

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The smtp check should greet and write mail like a mail client (loopback servers from proto_loopback_test.go).

func TestSmtpRun_ClientIdentity(t *testing.T) {
	t.Run("EHLO with one of the team's workstation names", func(t *testing.T) {
		srv := startSMTP(t, smtpBehaviour{})
		res := runCheck(t, &Smtp{Service: testService(srv.port, loopbackCred), Domain: "@acme.local"})
		require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)

		lines := waitSession(t, srv, 0, "QUIT")
		name, ok := strings.CutPrefix(lines[0], "EHLO ")
		require.True(t, ok, lines[0])
		assert.Contains(t, workstationPool("", "01", nil), name)
	})

	t.Run("configured HELO name", func(t *testing.T) {
		srv := startSMTP(t, smtpBehaviour{})
		res := runCheck(t, &Smtp{Service: testService(srv.port, loopbackCred), Domain: "@acme.local", HeloName: "ws-0412.acme.local"})
		require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
		assert.Equal(t, "EHLO ws-0412.acme.local", waitSession(t, srv, 0, "QUIT")[0])
	})

	t.Run("message carries client headers", func(t *testing.T) {
		srv := startSMTP(t, smtpBehaviour{})
		res := runCheck(t, &Smtp{Service: testService(srv.port, loopbackCred), Domain: "@acme.local"})
		require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
		waitSession(t, srv, 0, "QUIT")
		msgs := srv.messages()
		require.Len(t, msgs, 1)
		msg := strings.Join(msgs[0], "\n")
		for _, h := range []*regexp.Regexp{
			regexp.MustCompile(`(?m)^Message-ID: <[0-9a-f-]{36}@acme\.local>$`),
			regexp.MustCompile(`(?m)^Date: \w{3}, \d{2} \w{3} \d{4} \d{2}:\d{2}:\d{2} [+-]\d{4}$`),
			regexp.MustCompile(`(?m)^MIME-Version: 1\.0$`),
			regexp.MustCompile(`(?m)^From: <bugs\.bunny@acme\.local>$`),
			regexp.MustCompile(`(?m)^To: <bugs\.bunny@acme\.local>$`),
			regexp.MustCompile(`(?m)^Subject: \S`),
			regexp.MustCompile(`(?m)^Content-Type: text/plain; charset=UTF-8; format=flowed$`),
		} {
			assert.Regexp(t, h, msg)
		}
		assert.NotRegexp(t, `(?m)^Subject: [0-9a-f]{16}$`, msg)
		assert.NotRegexp(t, `(?m)^[0-9a-f]{64}$`, msg)
	})
}
