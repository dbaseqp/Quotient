package checks

import (
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"strings"
)

// workstationNameAlphabet is the character set of the random suffix Windows gives new computers (DESKTOP-XXXXXXX).
const workstationNameAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// workstationPool is a team's fixed set of 4-6 workstation names for the whole event: up to
// size-2 names from the event's workstation-name templates (MiscSettings.WorkstationNames, "_"
// replaced by the team identifier), the rest Windows-style DESKTOP-XXXXXXX names. It depends only
// on salt (the event name), the team and the templates, so every runner builds the same pool.
// The event checks' mailclient.py builds the same pool with the same algorithm.
func workstationPool(salt, team string, templates []string) []string {
	h := fnv64a(salt + "/" + team)
	size := 4 + int(h%3)
	n := min(len(templates), size-2)
	pool := make([]string, 0, size)
	for i := range n {
		pool = append(pool, strings.ReplaceAll(templates[(int(h>>8)+i)%len(templates)], "_", team))
	}
	for i := range size - n {
		x := fnv64a(salt + "/" + team + "/desktop/" + strconv.Itoa(i))
		var b [7]byte
		for j := range b {
			b[j] = workstationNameAlphabet[x%uint64(len(workstationNameAlphabet))]
			x /= uint64(len(workstationNameAlphabet))
		}
		pool = append(pool, "DESKTOP-"+string(b[:]))
	}
	return pool
}

// workstation picks the name of the PC this check claims to run on this round: one of the team's
// pool at random, as if different users' machines did the work. Protocols that carry a client
// host name (SMTP EHLO, NTLM's Workstation field) send it.
func (service *Service) workstation(team string) string {
	pool := workstationPool(service.WorkstationSalt, team, service.WorkstationNames)
	return pool[rand.IntN(len(pool))] // #nosec G404 -- non-crypto choice of a client name
}

// SetWorkstationNaming gives the check the event-wide inputs of the workstation-name pool.
func (service *Service) SetWorkstationNaming(salt string, templates []string) {
	service.WorkstationSalt = salt
	service.WorkstationNames = templates
}
