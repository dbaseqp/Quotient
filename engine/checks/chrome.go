package checks

import (
	"strconv"
	"strings"
)

// ChromeMajor is the Chrome release the web check and custom checks'
// browser_curl claim in their User-Agent and client hints. Python checks that
// impersonate Chrome through curl_cffi claim the release of their impersonate
// target instead. Keep it at the current stable release:
// TestChromeMajorIsCurrent fails once a newer release has been stable for more
// than 8 weeks, going by the pinned schedule in
// testdata/chrome_stable_schedule.json.
const ChromeMajor = 158

// chromeAcceptLanguage is the Accept-Language the web check and custom
// checks' browser_curl send.
const chromeAcceptLanguage = "en-US,en;q=0.9"

// chromeOS is how desktop Chrome names the OS it runs on: the OS part of its
// User-Agent and its sec-ch-ua-platform value, quoted the way Chrome sends it.
type chromeOS struct{ uaOS, platform string }

var (
	chromeWindows = chromeOS{"Windows NT 10.0; Win64; x64", `"Windows"`}
	chromeMacOS   = chromeOS{"Macintosh; Intel Mac OS X 10_15_7", `"macOS"`}
	chromeLinux   = chromeOS{"X11; Linux x86_64", `"Linux"`}
)

// chromeOSes are the desktop OSes a check run can claim.
var chromeOSes = []chromeOS{chromeWindows, chromeMacOS, chromeLinux}

// ChromeUserAgent is desktop Chrome's User-Agent for the given OS token, with
// the minor version reduced to 0.0.0 as Chrome sends it.
func ChromeUserAgent(uaOS string) string {
	return "Mozilla/5.0 (" + uaOS + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + strconv.Itoa(ChromeMajor) + ".0.0.0 Safari/537.36"
}

// ChromeSecCHUA builds the sec-ch-ua value the way Chromium does
// (GetUserAgentBrandList in components/embedder_support/user_agent_utils.cc):
// the GREASE brand, its version and the brand order all derive from the major
// version.
func ChromeSecCHUA(major int) string {
	greaseyChars := []string{" ", "(", ":", "-", ".", "/", ")", ";", "=", "?", "_"}
	greasedVersions := []string{"8", "99", "24"}
	orders := [6][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	v := strconv.Itoa(major)
	grease := `"Not` + greaseyChars[major%len(greaseyChars)] + "A" + greaseyChars[(major+1)%len(greaseyChars)] + `Brand";v="` + greasedVersions[major%len(greasedVersions)] + `"`
	var brands [3]string
	order := orders[major%len(orders)]
	brands[order[0]] = grease
	brands[order[1]] = `"Chromium";v="` + v + `"`
	brands[order[2]] = `"Google Chrome";v="` + v + `"`
	return strings.Join(brands[:], ", ")
}
