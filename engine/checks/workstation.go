package checks

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// workstationNameAlphabet is the character set of the random suffix Windows gives new computers (DESKTOP-XXXXXXX).
const workstationNameAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// workstationName returns a Windows-style computer name that stays the same for a team's check
// across rounds, like a real client on one PC. Protocols that carry a client host name (NTLM's
// Workstation field) would otherwise send an empty one.
func workstationName(teamID uint, checkName string) string {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d/%s", teamID, checkName)
	v := h.Sum64()
	var b strings.Builder
	b.WriteString("DESKTOP-")
	for range 7 {
		b.WriteByte(workstationNameAlphabet[v%uint64(len(workstationNameAlphabet))])
		v /= uint64(len(workstationNameAlphabet))
	}
	return b.String()
}
