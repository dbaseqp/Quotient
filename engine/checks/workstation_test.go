package checks

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var desktopName = regexp.MustCompile(`^DESKTOP-[A-Z0-9]{7}$`)

func TestWorkstationPool(t *testing.T) {
	sizes := map[int]bool{}
	for i := range 60 {
		team := fmt.Sprintf("%02d", i)
		pool := workstationPool("Inv3", team, nil)
		require.Equal(t, pool, workstationPool("Inv3", team, nil), "the pool is fixed for the event")
		require.GreaterOrEqual(t, len(pool), 4)
		require.LessOrEqual(t, len(pool), 6)
		sizes[len(pool)] = true
		seen := map[string]bool{}
		for _, n := range pool {
			assert.Regexp(t, desktopName, n, "no templates: all Windows-style")
			assert.False(t, seen[n], "duplicate name %s", n)
			seen[n] = true
		}
	}
	assert.Len(t, sizes, 3, "pool sizes 4, 5 and 6 all occur")
	assert.NotEqual(t, workstationPool("Inv3", "01", nil), workstationPool("Inv3", "02", nil), "teams get their own names")
	assert.NotEqual(t, workstationPool("Inv3", "01", nil), workstationPool("Inv4", "01", nil), "events get their own names")

	tpl := []string{"WS_-A", "LAB_PC", "T_-FRONTDESK", "T_-HR"}
	pool := workstationPool("Inv3", "07", tpl)
	var fromTpl, desktop int
	for _, n := range pool {
		switch {
		case desktopName.MatchString(n):
			desktop++
		case n == "WS07-A" || n == "LAB07PC" || n == "T07-FRONTDESK" || n == "T07-HR":
			fromTpl++
		default:
			t.Errorf("name %q is neither a template with the team filled in nor Windows-style", n)
		}
	}
	assert.GreaterOrEqual(t, desktop, 2, "the blend keeps Windows-style names")
	assert.Equal(t, min(len(tpl), len(pool)-2), fromTpl, "templates fill the rest of the pool")
}

// TestWorkstationPoolGolden pins the algorithm that mailclient.py in the event checks shares.
func TestWorkstationPoolGolden(t *testing.T) {
	assert.Equal(t, goldenNoTemplates, workstationPool("acme-anvil", "01", nil))
	assert.Equal(t, goldenTemplates, workstationPool("acme-anvil", "01", []string{"WS_-A", "LAB_PC"}))
}

func TestWorkstationPicksFromPoolEachRound(t *testing.T) {
	s := &Service{}
	s.SetWorkstationNaming("Inv3", []string{"WS_-A"})
	pool := workstationPool("Inv3", "03", []string{"WS_-A"})
	picked := map[string]bool{}
	for range 300 {
		n := s.workstation("03")
		require.Contains(t, pool, n)
		picked[n] = true
	}
	assert.Len(t, picked, len(pool), "every pool name gets used")
}

var (
	goldenNoTemplates = []string{"DESKTOP-88HZM9T", "DESKTOP-R9JTJDV", "DESKTOP-67DBT1R", "DESKTOP-P8F5P5S"}
	goldenTemplates   = []string{"LAB01PC", "WS01-A", "DESKTOP-88HZM9T", "DESKTOP-R9JTJDV"}
)
