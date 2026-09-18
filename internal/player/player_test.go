package player_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/player"
)

/*
The two handles leave the same bytes behind.

Nothing here asserts a number this wrote. A write to a player is a write into a
running game, and being one field out is not an error message: it lands in
whatever the game keeps next door. So each case runs the same operations over
the same planted memory in both languages and compares the *whole buffer*,
which catches a field written in the wrong place as surely as a field written
with the wrong value.
*/

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

const (
	base = 0x10000000
	size = 0x1000
	life = base + 0x800
)

// preamble plants the same player in the Python's fake as the Go tests plant in
// theirs, and leaves a handle on it.
const preamble = `
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker.player import Player
mem = FakeMem(0x10000000, 0x1000)
mem.plant_player(0x10000800, [380, 400, 137, 201, 220, 180], 0)
p = Player(mem, 0x10000800)
`

/*
plant is a Go fake holding the same player.

All six numbers differ from each other on purpose. A block of equal values reads
the same from the wrong offset as from the right one, which is exactly the
mistake these tests exist to catch.
*/
func plant(t *testing.T) (*memtest.FakeMem, *player.Player) {
	t.Helper()
	mem := memtest.New(base, size)
	mem.PlantPlayer(life, []int32{380, 400, 137, 201, 220, 180}, 0)
	return mem, player.New(mem, life)
}

// Every field is read from the same place.
func TestReadingAPlayerMatchesThePython(t *testing.T) {
	var want map[string]int32
	askPython(t, preamble+`print(json.dumps({
    "life": p.stat_life, "life_max": p.stat_life_max, "life_max2": p.stat_life_max2,
    "mana": p.stat_mana, "mana_max": p.stat_mana_max, "mana_max2": p.stat_mana_max2,
}))`, &want)

	_, p := plant(t)
	for name, read := range map[string]func() (int32, bool){
		"life":      p.StatLife,
		"life_max":  p.StatLifeMax,
		"life_max2": p.StatLifeMax2,
		"mana":      p.StatMana,
		"mana_max":  p.StatManaMax,
		"mana_max2": p.StatManaMax2,
	} {
		got, ok := read()
		require.Truef(t, ok, "%s could not be read", name)
		require.Equalf(t, want[name], got, "%s is read from somewhere else", name)
	}
}

// Every write leaves the buffer in the same state, byte for byte.
func TestWritingAPlayerMatchesThePython(t *testing.T) {
	cases := []struct {
		name   string
		python string
		run    func(p *player.Player) bool
	}{
		{"set_life", "p.set_life(1)", func(p *player.Player) bool { return p.SetLife(1) }},
		{"set_mana", "p.set_mana(7)", func(p *player.Player) bool { return p.SetMana(7) }},
		{"set_max_life", "p.set_max_life(500)", func(p *player.Player) bool { return p.SetMaxLife(500) }},
		{"set_max_mana", "p.set_max_mana(400)", func(p *player.Player) bool { return p.SetMaxMana(400) }},
		{"heal_full", "p.heal_full()", func(p *player.Player) bool { return p.HealFull() }},
		{"mana_full", "p.mana_full()", func(p *player.Player) bool { return p.ManaFull() }},
		// Raising the cap and then filling to it is the sequence the trainer
		// actually performs, and it is where writing only the permanent field
		// would leave the fill short.
		{"max then full", "p.set_max_life(500); p.heal_full()", func(p *player.Player) bool {
			return p.SetMaxLife(500) && p.HealFull()
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				Buf string `json:"buf"`
				OK  bool   `json:"ok"`
			}
			askPython(t, preamble+c.python+`
print(json.dumps({"buf": mem.buf.hex(), "ok": True}))`, &want)

			mem, p := plant(t)
			require.True(t, c.run(p), "the write was refused")
			require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
		})
	}
}

/*
A player whose memory has gone is reported, not guessed at.

The object moves when the managed heap collects, and an address that stops
reading is how that shows. Filling to a cap that could not be read would write
whatever the last value happened to be.
*/
func TestAnUnreadablePlayerIsRefused(t *testing.T) {
	var want map[string]any
	askPython(t, preamble+`
gone = Player(mem, 0x20000000)
print(json.dumps({"life": gone.stat_life, "heal": gone.heal_full(),
                  "mana": gone.mana_full(), "set": gone.set_life(1)}))`, &want)
	require.Nil(t, want["life"], "the Python read a player that is not mapped")

	mem, _ := plant(t)
	gone := player.New(mem, 0x20000000)
	_, ok := gone.StatLife()
	require.False(t, ok, "life was read from unmapped memory")
	require.Equal(t, want["heal"], gone.HealFull(), "healing disagrees")
	require.Equal(t, want["mana"], gone.ManaFull(), "filling mana disagrees")
	require.Equal(t, want["set"], gone.SetLife(1), "writing disagrees")
}
