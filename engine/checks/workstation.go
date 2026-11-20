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
func workstationPool(salt, team string, templates []string) []string {
	h := fnv64a(salt + "/" + team)
	size := 4 + int(h%3)
	n := min(len(templates), size-2)
	pool := make([]string, 0, size)
	for i := range n {
		pool = append(pool, strings.ReplaceAll(templates[((h>>8)+uint64(i))%uint64(len(templates))], "_", team))
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

// workstationNaming holds the event-wide inputs of the workstation-name pool, for checks that
// send a client host name. The config sets them; they reach the runner in the check's JSON.
type workstationNaming struct {
	WorkstationSalt  string   `toml:"-"` // Event name: seeds each team's workstation-name pool
	WorkstationNames []string `toml:"-"` // MiscSettings.WorkstationNames templates
}

// workstation picks one name from the team's pool at random, on every check attempt.
func (w *workstationNaming) workstation(team string) string {
	pool := workstationPool(w.WorkstationSalt, team, w.WorkstationNames)
	return pool[rand.IntN(len(pool))] // #nosec G404 -- non-crypto choice of a client name
}

// SetWorkstationNaming gives the check the event-wide inputs of the workstation-name pool.
func (w *workstationNaming) SetWorkstationNaming(salt string, templates []string) {
	w.WorkstationSalt = salt
	w.WorkstationNames = templates
}
