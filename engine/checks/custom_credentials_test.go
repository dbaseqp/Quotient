package checks

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCustom runs a Custom check with the given task credentials and returns the command output and
// the result. The output is the text after " in " in Debug (the regex-matched output).
func runCustom(t *testing.T, credlists []string, creds []TaskCredential, command string) (string, Result) {
	t.Helper()
	check := &Custom{
		Service: Service{Name: "test-custom", Target: "127.0.0.1", Timeout: 5, CredLists: credlists},
		Command: command,
		Regex:   "RESULT:.*",
	}
	check.SetTaskCredentials(creds)

	resultsChan := make(chan Result, 1)
	check.Run(1, "01", 1, resultsChan)
	select {
	case result := <-resultsChan:
		_, out, _ := strings.Cut(result.Debug, " in ")
		return strings.TrimSpace(out), result
	case <-time.After(10 * time.Second):
		t.Fatal("check timed out")
		return "", Result{}
	}
}

func TestCustomPlainCredentialsNeverComeFromANamedCredlist(t *testing.T) {
	creds := []TaskCredential{
		{Username: "alice", Password: "alice-pw", Credlist: "ad"},
		{Username: "bob", Password: "bob-pw", Credlist: "ad"},
		{Username: "api", Password: "the-api-key", Credlist: "api"},
	}
	for range 40 {
		out, result := runCustom(t, []string{"ad", "api"}, creds, `echo "RESULT:USERNAME PASSWORD PASSWORD[api]"`)
		require.True(t, result.Status, result.Debug)
		fields := strings.Fields(strings.TrimPrefix(out, "RESULT:"))
		require.Len(t, fields, 3, out)
		assert.Contains(t, []string{"alice", "bob"}, fields[0])
		assert.Contains(t, []string{"alice-pw", "bob-pw"}, fields[1])
		assert.Equal(t, "the-api-key", fields[2])
	}
}

func TestCustomNamedCredlistAloneNeedsNoPlainCredentials(t *testing.T) {
	creds := []TaskCredential{{Username: "api", Password: "the-api-key", Credlist: "api"}}
	out, result := runCustom(t, []string{"api"}, creds, `echo "RESULT:PASSWORD[api]"`)
	require.True(t, result.Status, result.Debug)
	assert.Equal(t, "RESULT:the-api-key", out)
}

func TestCustomMissingNamedCredlistFailsTheCheck(t *testing.T) {
	creds := []TaskCredential{{Username: "alice", Password: "alice-pw", Credlist: "ad"}}
	_, result := runCustom(t, []string{"ad", "api"}, creds, `echo "RESULT:PASSWORD[api]"`)
	assert.False(t, result.Status)
	assert.Equal(t, "error getting creds", result.Error)
	assert.Contains(t, result.Debug, `"api"`)
}

// A team controls its passwords, so a value must never be scanned for placeholders or reach the shell.
func TestCustomCredentialValuesAreNotRescannedOrExecuted(t *testing.T) {
	t.Run("named value", func(t *testing.T) {
		hostile := `PASSWORD USERNAME TARGET ROUND $(echo injected) ` + "`echo injected`" + ` ' ; echo injected`
		creds := []TaskCredential{
			{Username: "alice", Password: "alice-pw", Credlist: "ad"},
			{Username: "api", Password: hostile, Credlist: "api"},
		}
		out, result := runCustom(t, []string{"ad", "api"}, creds, `printf 'RESULT:%s' PASSWORD[api]`)
		require.True(t, result.Status, result.Debug)
		assert.Equal(t, "RESULT:"+hostile, out)
	})
	t.Run("plain value naming another credlist", func(t *testing.T) {
		creds := []TaskCredential{
			{Username: "alice", Password: "PASSWORD[api]", Credlist: "ad"},
			{Username: "api", Password: "the-api-key", Credlist: "api"},
		}
		out, result := runCustom(t, []string{"ad", "api"}, creds, `printf 'RESULT:%s %s' PASSWORD PASSWORD[api]`)
		require.True(t, result.Status, result.Debug)
		assert.Equal(t, "RESULT:PASSWORD[api] the-api-key", out)
	})
	t.Run("username naming a placeholder", func(t *testing.T) {
		creds := []TaskCredential{{Username: "PASSWORD", Password: "alice-pw", Credlist: "ad"}}
		out, result := runCustom(t, []string{"ad"}, creds, `printf 'RESULT:%s %s' USERNAME PASSWORD`)
		require.True(t, result.Status, result.Debug)
		assert.Equal(t, "RESULT:PASSWORD alice-pw", out)
	})
}

func TestCustomVerifyRejectsCredentialPlaceholdersThatCannotWork(t *testing.T) {
	cases := []struct {
		name      string
		credlists []string
		command   string
		wantErr   string
	}{
		{"unterminated", []string{"ad", "api"}, "c.sh PASSWORD[api", "PASSWORD["},
		{"space in the name", []string{"ad", "api key"}, "c.sh PASSWORD[api key]", "PASSWORD["},
		{"empty name", []string{"ad"}, "c.sh PASSWORD[]", "PASSWORD["},
		{"every credlist is read by name", []string{"api"}, "c.sh USERNAME PASSWORD[api]", "every one of its credlists"},
		{"every credlist is read by name, listed twice", []string{"api", "api"}, "c.sh USERNAME PASSWORD[api]", "every one of its credlists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := &Custom{Service: Service{CredLists: tc.credlists}, Command: tc.command}
			err := check.Verify("box", "10.0.0.1", 1, 5, 0, 0)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	// Only the named credlist needs no plain credentials, so this is fine.
	check := &Custom{Service: Service{CredLists: []string{"api"}}, Command: "c.sh PASSWORD[api]"}
	assert.NoError(t, check.Verify("box", "10.0.0.1", 1, 5, 0, 0))
}

func TestCustomVerifyRejectsACredlistTheCheckDoesNotList(t *testing.T) {
	check := &Custom{
		Service: Service{CredLists: []string{"ad"}},
		Command: "/app/checks/c.sh PASSWORD[api]",
	}
	err := check.Verify("box", "10.0.0.1", 1, 5, 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PASSWORD[api]")

	check = &Custom{
		Service: Service{CredLists: []string{"ad", "api"}},
		Command: "/app/checks/c.sh USERNAME PASSWORD PASSWORD[api]",
	}
	assert.NoError(t, check.Verify("box", "10.0.0.1", 1, 5, 0, 0))
}
