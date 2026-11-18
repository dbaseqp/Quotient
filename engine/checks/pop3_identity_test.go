package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pop3 check should talk like a mail client: CAPA first, no NOOP after PASS.
func TestPop3Run_ClientIdentity(t *testing.T) {
	srv := startPOP3(t, pop3Behaviour{})
	res := runCheck(t, &Pop3{Service: testService(srv.port, loopbackCred)})
	require.True(t, res.Status, "debug: %s, error: %s", res.Debug, res.Error)
	assert.Equal(t, []string{"CAPA", "USER bugs.bunny", "PASS Carrots1!", "STAT", "QUIT"}, waitSession(t, srv, 0, "QUIT"))
}
