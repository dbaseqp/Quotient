package checks

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every run claims ChromeMajor on Linux, the OS whose network stack sends
// the check's packets, with the web check's Accept-Language.
func TestChromeEnvClaimsLinux(t *testing.T) {
	env := map[string]string{}
	for _, kv := range ChromeEnv() {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, kv)
		env[k] = v
	}
	assert.Equal(t, map[string]string{
		"QUOTIENT_CHROME_USER_AGENT": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + strconv.Itoa(ChromeMajor) + ".0.0.0 Safari/537.36",
		"QUOTIENT_CHROME_PLATFORM":   `"Linux"`,
		"QUOTIENT_CHROME_SEC_CH_UA":  ChromeSecCHUA(ChromeMajor),
		"QUOTIENT_CHROME_LANGUAGE":   "en-US,en;q=0.9",
	}, env)
}
