package locate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
The locator is compared with the Python's over the same planted memory.

Being wrong here is not an error message. The addresses this returns are written
to, so a rule that is looser than the Python's finds something that is not a
player and the trainer edits it; a rule that is tighter misses the live copy and
every write lands on an inert snapshot the game ignores -- which has happened,
and is why the boost headroom exists.

So nothing here asserts what the Go produced. Every answer is the Python's.
*/

const pythonTimeout = 2 * time.Minute

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

// blocks are the cases a life and mana block is judged on: the ordinary ones,
// and every edge the rule has a reason for.
var blocks = [][]int32{
	{400, 400, 400, 200, 200, 200}, // a plain, full player
	{420, 400, 420, 220, 200, 220}, // boosted caps, current at the boosted one
	{400, 400, 1, 0, 200, 200},     // one hit point and no mana, which is legal
	{400, 400, 401, 200, 200, 200}, // life above the boosted cap
	{400, 400, 0, 200, 200, 200},   // no life at all: not a living player
	{400, 402, 400, 200, 200, 200}, // a cap that is not a multiple of five
	{400, 95, 95, 200, 200, 200},   // below the smallest real cap
	{400, 505, 505, 200, 200, 200}, // above the largest
	{901, 400, 400, 200, 200, 200}, // boosted life beyond the headroom
	{400, 400, 400, 200, 210, 210}, // a mana cap that is not a multiple of twenty
	{400, 400, 400, 200, 420, 420}, // a mana cap beyond the largest
	{400, 400, 400, 221, 200, 220}, // mana above the boosted cap
	{400, 400, 400, -1, 200, 200},  // negative mana
	{0, 0, 0, 0, 0, 0},             // empty memory
	{-1, -1, -1, -1, -1, -1},       // and memory that is not a block at all
}

// Every rule about what a player's block looks like is the Python's rule.
func TestValidBlockMatchesThePython(t *testing.T) {
	var cases []string
	for _, b := range blocks {
		cases = append(cases, fmt.Sprintf("[%d,%d,%d,%d,%d,%d]", b[0], b[1], b[2], b[3], b[4], b[5]))
	}
	var want []bool
	askPython(t, `
import json
from terrariabonker import locate
print(json.dumps([locate.valid_block(b) for b in [`+strings.Join(cases, ",")+`]]))
`, &want)

	require.Len(t, want, len(blocks))
	for i, b := range blocks {
		require.Equalf(t, want[i], locate.ValidBlock(b), "block %v is judged differently", b)
	}
	require.False(t, locate.ValidBlock([]int32{1, 2, 3}), "too few numbers is not a block")
}

/*
A name is only a name if it reads like one.

Any four bytes of heap can be read as a pointer; very little of what they point
at is a printable ASCII string of a plausible length. That is what turns a
coincidence into a match.
*/
func TestReadMonoStringMatchesThePython(t *testing.T) {
	const base, size = 0x10000000, 0x400
	names := []string{"Nakama", "a", strings.Repeat("x", 64), "has space", "Zoë"}

	for _, name := range names {
		mem := memtest.New(base, size)
		mem.PlantMonoString(base+0x40, name)

		var want any
		askPython(t, preamble+`
mem.plant_mono_string(0x10000040, `+quote(name)+`)
print(json.dumps(locate.read_mono_string(mem, 0x10000040)))
`, &want)

		got, ok := locate.ReadMonoString(mem, base+0x40)
		if want == nil {
			require.Falsef(t, ok, "%q is a name here and not there", name)
			continue
		}
		require.Truef(t, ok, "%q is a name there and not here", name)
		require.Equal(t, want, got)
	}

	// A length nothing could be, and a pointer into nothing.
	mem := memtest.New(base, size)
	mem.PokeI32(base+0x40+8, 999)
	_, ok := locate.ReadMonoString(mem, base+0x40)
	require.False(t, ok, "a length no name has")
	_, ok = locate.ReadMonoString(mem, base+uint32(size))
	require.False(t, ok, "and a pointer past the end of the region")
}

/*
A scan of the same memory finds the same players.

The whole point of the module: the same buffer in, the same addresses and names
out. A player is planted twice -- the live copy and a snapshot, which is what a
real game looks like -- plus a block that passes the cheap prefilter and has no
name behind it, which is what random memory looks like.
*/
func TestFindPlayersMatchesThePython(t *testing.T) {
	const base, size = 0x10000000, 0x4000

	mem := memtest.New(base, size)
	mem.PlantMonoString(base+0x40, "Nakama")
	mem.PlantPlayer(base+0x800, []int32{420, 400, 420, 220, 200, 220}, base+0x40)
	mem.PlantPlayer(base+0x1800, []int32{400, 400, 400, 200, 200, 200}, base+0x40)
	// A near miss: it passes the prefilter and has nothing readable where a
	// name pointer would be.
	mem.PokeI32(base+0x2000, 400)
	mem.PokeI32(base+0x2004, 400)
	mem.PokeI32(base+0x2008, 400)

	var want []map[string]any
	askPython(t, preamble+`
mem.plant_mono_string(0x10000040, "Nakama")
mem.plant_player(0x10000800, [420, 400, 420, 220, 200, 220], 0x10000040)
mem.plant_player(0x10001800, [400, 400, 400, 200, 200, 200], 0x10000040)
mem.poke_i32(0x10002000, 400)
mem.poke_i32(0x10002004, 400)
mem.poke_i32(0x10002008, 400)
print(json.dumps([
    {"addr": p.life_addr, "name": p.name, "block": p.block}
    for p in locate.find_players(mem)
]))
`, &want)

	got := locate.FindPlayers(mem)
	require.Len(t, got, len(want), "a different number of players was found")
	require.NotEmpty(t, got, "and neither scan found none")

	for i, w := range want {
		require.Equalf(t, uint32(w["addr"].(float64)), got[i].LifeAddr, "player %d is somewhere else", i)
		require.Equalf(t, w["name"], got[i].Name, "player %d is called something else", i)
		block := w["block"].([]any)
		for j, field := range got[i].Fields() {
			require.Equalf(t, int32(block[j].(float64)), field, "player %d field %d differs", i, j)
		}
	}
}

// Reading a block at a known address agrees too, including where there is no
// player: a cached address being re-checked has to report that it has gone.
func TestReadBlockMatchesThePython(t *testing.T) {
	const base, size = 0x10000000, 0x4000
	mem := memtest.New(base, size)
	mem.PlantMonoString(base+0x40, "Nakama")
	mem.PlantPlayer(base+0x800, []int32{400, 400, 400, 200, 200, 200}, base+0x40)

	for _, addr := range []uint32{base + 0x800, base + 0x900, base + 0x3FFF} {
		var want any
		askPython(t, preamble+`
mem.plant_mono_string(0x10000040, "Nakama")
mem.plant_player(0x10000800, [400, 400, 400, 200, 200, 200], 0x10000040)
got = locate.read_block(mem, `+fmt.Sprintf("%d", addr)+`)
print(json.dumps(None if got is None else {"addr": got.life_addr, "name": got.name}))
`, &want)

		block, ok := locate.ReadBlock(mem, addr)
		if want == nil {
			require.Falsef(t, ok, "%#x is a player here and not there", addr)
			continue
		}
		require.Truef(t, ok, "%#x is a player there and not here", addr)
		require.Equal(t, "Nakama", block.Name)
	}
}

// preamble builds the Python's fake and imports the locator.
const preamble = `
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import locate
mem = FakeMem(0x10000000, 0x4000)
`

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

/*
The guess at which copy is live is the Python's guess.

It is only reached when the resolver cannot answer, and it is wrong often enough
that the comments say so -- but "wrong the same way in both languages" is what
this port owes, because a caller that changed its mind about which copy to write
to would start editing a corpse.

Nothing here moves while it is sampled: a planted buffer is as frozen as a
paused game, which is the case the fallbacks exist for and the one that actually
happens.
*/
func TestPickingTheLiveCopyMatchesThePython(t *testing.T) {
	cases := []struct {
		name   string
		python string
		plant  func(mem *memtest.FakeMem)
	}{
		{"one copy", `
mem.plant_player(0x10000800, [400, 400, 400, 200, 200, 200], 0x10000040)`,
			func(mem *memtest.FakeMem) {
				mem.PlantPlayer(0x10000800, []int32{400, 400, 400, 200, 200, 200}, 0x10000040)
			}},
		// Two frozen copies, one of them hurt: the only one below its cap wins.
		{"one below its cap", `
mem.plant_player(0x10000800, [400, 400, 400, 200, 200, 200], 0x10000040)
mem.plant_player(0x10001800, [400, 400, 137, 200, 200, 200], 0x10000040)`,
			func(mem *memtest.FakeMem) {
				mem.PlantPlayer(0x10000800, []int32{400, 400, 400, 200, 200, 200}, 0x10000040)
				mem.PlantPlayer(0x10001800, []int32{400, 400, 137, 200, 200, 200}, 0x10000040)
			}},
		// Both hurt: there is nothing to tell them apart, so neither is picked.
		{"both below", `
mem.plant_player(0x10000800, [400, 400, 300, 200, 200, 200], 0x10000040)
mem.plant_player(0x10001800, [400, 400, 137, 200, 200, 200], 0x10000040)`,
			func(mem *memtest.FakeMem) {
				mem.PlantPlayer(0x10000800, []int32{400, 400, 300, 200, 200, 200}, 0x10000040)
				mem.PlantPlayer(0x10001800, []int32{400, 400, 137, 200, 200, 200}, 0x10000040)
			}},
		// Both at full life, which is the paused idle player: give up.
		{"neither below", `
mem.plant_player(0x10000800, [400, 400, 400, 200, 200, 200], 0x10000040)
mem.plant_player(0x10001800, [400, 400, 400, 200, 200, 200], 0x10000040)`,
			func(mem *memtest.FakeMem) {
				mem.PlantPlayer(0x10000800, []int32{400, 400, 400, 200, 200, 200}, 0x10000040)
				mem.PlantPlayer(0x10001800, []int32{400, 400, 400, 200, 200, 200}, 0x10000040)
			}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want any
			askPython(t, preamble+`
mem.plant_mono_string(0x10000040, "Nakama")`+c.python+`
got = locate.pick_live(mem, locate.find_players(mem), samples=2, dt=0)
print(json.dumps(None if got is None else got.life_addr))`, &want)

			mem := memtest.New(0x10000000, 0x4000)
			mem.PlantMonoString(0x10000040, "Nakama")
			c.plant(mem)

			got, ok := locate.PickLive(mem, locate.FindPlayers(mem), 2, 0)
			if want == nil {
				require.False(t, ok, "a copy was picked here and not there")
				return
			}
			require.True(t, ok, "a copy was picked there and not here")
			require.Equal(t, uint32(want.(float64)), got.LifeAddr, "a different copy")
		})
	}
}
