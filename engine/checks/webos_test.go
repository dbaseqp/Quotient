package checks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A profile's sockets must announce the window settings of the OS its
// User-Agent names: a Windows User-Agent over a macOS window is easier to
// spot than either alone.
func TestBrowserProfileOSMatchesUserAgent(t *testing.T) {
	for _, p := range browserProfiles {
		ua := p.userAgent()
		want := clientLinux
		switch {
		case strings.Contains(ua, "Windows NT"):
			want = clientWindows
		case strings.Contains(ua, "Macintosh"):
			want = clientMacOS
		}
		assert.Equal(t, want, p.os, "%s: User-Agent %q", p.name, ua)
	}
}
