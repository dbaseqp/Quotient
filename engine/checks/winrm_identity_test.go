package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The winrm check should send the Windows WinRM client's HTTP identity.
func TestWinRMRun_ClientIdentity(t *testing.T) {
	srv := startWinRM(t, "Carrots1!", "WIN-SRV01\r\n")
	res := runCheck(t, &WinRM{Service: testService(srv.port, loopbackCred)})
	require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
	reqs := srv.headers()
	require.NotEmpty(t, reqs)
	for i, h := range reqs {
		assert.Equal(t, winrmUserAgent, h.Get("User-Agent"), "request %d", i)
		assert.Empty(t, h.Get("Accept-Encoding"), "request %d", i)
		assert.Equal(t, "application/soap+xml;charset=UTF-8", h.Get("Content-Type"), "request %d", i)
	}
}
