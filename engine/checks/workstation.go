package checks

import (
	crand "crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
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
// on seed (the event's secret WorkstationSeed), the team and the templates, so every runner
// builds the same pool and a team cannot compute it without the seed.
func workstationPool(seed, team string, templates []string) []string {
	h := fnv64a(seed + "/" + team)
	size := 4 + int(h%3)
	n := min(len(templates), size-2)
	pool := make([]string, 0, size)
	for i := range n {
		pool = append(pool, strings.ReplaceAll(templates[((h>>8)+uint64(i))%uint64(len(templates))], "_", team))
	}
	for i := range size - n {
		x := fnv64a(seed + "/" + team + "/desktop/" + strconv.Itoa(i))
		var b [7]byte
		for j := range b {
			b[j] = workstationNameAlphabet[x%uint64(len(workstationNameAlphabet))]
			x /= uint64(len(workstationNameAlphabet))
		}
		pool = append(pool, "DESKTOP-"+string(b[:]))
	}
	return pool
}

// processWorkstationSeed seeds the pools when MiscSettings.WorkstationSeed is empty. It lasts for
// the process, so config reloads keep the pools, but a restart gives every team new names.
var processWorkstationSeed = sync.OnceValue(func() string {
	var b [32]byte
	_, _ = crand.Read(b[:]) // never fails: crypto/rand.Read aborts the program instead
	slog.Warn("MiscSettings.WorkstationSeed is not set: the teams' workstation names change on every engine restart")
	return hex.EncodeToString(b[:])
})

// workstationNaming holds the event-wide inputs of the workstation-name pool, for checks that
// send a client host name. The config sets them; they reach the runner in the check's JSON.
type workstationNaming struct {
	WorkstationSeed  string   `toml:"-"` // Secret seed of each team's workstation-name pool
	WorkstationNames []string `toml:"-"` // MiscSettings.WorkstationNames templates
}

// workstation picks one name from the team's pool at random, on every check attempt.
func (w *workstationNaming) workstation(team string) string {
	pool := workstationPool(w.WorkstationSeed, team, w.WorkstationNames)
	return pool[rand.IntN(len(pool))] // #nosec G404 -- non-crypto choice of a client name
}

// SetWorkstationNaming gives the check the event-wide inputs of the workstation-name pool: the
// configured seed (the per-process random seed when it is empty) and the name templates.
func (w *workstationNaming) SetWorkstationNaming(seed string, templates []string) {
	if seed == "" {
		seed = processWorkstationSeed()
	}
	w.WorkstationSeed = seed
	w.WorkstationNames = templates
}
