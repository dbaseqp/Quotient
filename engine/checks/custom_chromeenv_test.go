package checks

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseChromeEnv returns ChromeEnv's variables by name.
func parseChromeEnv(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, kv := range ChromeEnv() {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, kv)
		env[k] = v
	}
	return env
}

// Every run claims ChromeMajor on one of chromeOSes, with that OS's
// User-Agent and platform together, and one of chromeLanguages; over many
// runs each OS and language comes up.
func TestChromeEnvPicksOneIdentityPerRun(t *testing.T) {
	seenOS := map[chromeOS]bool{}
	seenLanguage := map[string]bool{}
	for range 300 {
		env := parseChromeEnv(t)
		require.Len(t, env, 4)
		assert.Equal(t, ChromeSecCHUA(ChromeMajor), env["QUOTIENT_CHROME_SEC_CH_UA"])
		i := slices.IndexFunc(chromeOSes, func(o chromeOS) bool {
			return ChromeUserAgent(o.uaOS) == env["QUOTIENT_CHROME_USER_AGENT"] && o.platform == env["QUOTIENT_CHROME_PLATFORM"]
		})
		require.GreaterOrEqual(t, i, 0, "User-Agent %q and platform %q are not one of chromeOSes", env["QUOTIENT_CHROME_USER_AGENT"], env["QUOTIENT_CHROME_PLATFORM"])
		seenOS[chromeOSes[i]] = true
		require.Contains(t, chromeLanguages, env["QUOTIENT_CHROME_LANGUAGE"])
		seenLanguage[env["QUOTIENT_CHROME_LANGUAGE"]] = true
	}
	assert.Len(t, seenOS, len(chromeOSes))
	assert.Len(t, seenLanguage, len(chromeLanguages))
}
