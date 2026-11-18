package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Anonymous FTP logins should use an e-mail style password, as FTP clients do.
func TestFtpRun_AnonymousPassword(t *testing.T) {
	srv := startFTP(t, false)
	res := runCheck(t, &Ftp{Service: testService(srv.port), File: []FtpFile{{Name: "flag.txt", Regex: "flag"}}})
	require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
	lines := waitSession(t, srv, 0, "QUIT")
	assert.Equal(t, []string{"USER anonymous", "PASS anonymous@example.com"}, lines[:2])
}
