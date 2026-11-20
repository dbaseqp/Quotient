package checks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"strconv"
	"strings"
)

// workstationNameAlphabet is the character set of the random suffix Windows gives new computers (DESKTOP-XXXXXXX).
const workstationNameAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// seededUint64 is the first 8 bytes, big-endian, of HMAC-SHA256(seed, msg).
func seededUint64(seed, msg string) uint64 {
	mac := hmac.New(sha256.New, []byte(seed))
	_, _ = mac.Write([]byte(msg))
	return binary.BigEndian.Uint64(mac.Sum(nil))
}

// workstationPool is a team's fixed set of 4-6 workstation names for the whole event: up to
// size-2 names from the event's workstation-name templates (MiscSettings.WorkstationNames, "_"
// replaced by the team identifier), the rest Windows-style DESKTOP-XXXXXXX names. It depends only
// on seed (the event's WorkstationSeed), the team and the templates, so every runner builds the
// same pool. Every value comes from HMAC-SHA256 keyed by the seed: names observed for some teams
// reveal nothing about other teams' pools to anyone without the seed.
func workstationPool(seed, team string, templates []string) []string {
	h := seededUint64(seed, team)
	size := 4 + int(h%3)
	n := min(len(templates), size-2)
	pool := make([]string, 0, size)
	for i := range n {
		pool = append(pool, strings.ReplaceAll(templates[((h>>8)+uint64(i))%uint64(len(templates))], "_", team))
	}
	for i := range size - n {
		x := seededUint64(seed, team+"|desktop|"+strconv.Itoa(i))
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
	WorkstationSeed  string   `toml:"-"` // Seed of each team's workstation-name pool
	WorkstationNames []string `toml:"-"` // MiscSettings.WorkstationNames templates
}

// workstation picks one name from the team's pool at random, on every check attempt.
func (w *workstationNaming) workstation(team string) string {
	pool := workstationPool(w.WorkstationSeed, team, w.WorkstationNames)
	return pool[rand.IntN(len(pool))] // #nosec G404 -- non-crypto choice of a client name
}

// SetWorkstationNaming gives the check the event-wide inputs of the workstation-name pool: the
// seed the config resolved and the name templates.
func (w *workstationNaming) SetWorkstationNaming(seed string, templates []string) {
	w.WorkstationSeed = seed
	w.WorkstationNames = templates
}
