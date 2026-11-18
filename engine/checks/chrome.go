package checks

import (
	"strconv"
	"strings"
)

// ChromeMajor is the one Chrome release every check that claims to be Chrome
// sends: the web check's User-Agent and client hints, and custom checks'
// browser_curl through QUOTIENT_CHROME_MAJOR and QUOTIENT_CHROME_SEC_CH_UA.
// Keep it at the current stable release: TestChromeMajorIsCurrent fails once
// a newer release has been stable for more than 8 weeks, going by the pinned
// schedule in testdata/chrome_stable_schedule.json.
const ChromeMajor = 158

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
	brands[order[2]] = `"Chrome";v="` + v + `"`
	return strings.Join(brands[:], ", ")
}
