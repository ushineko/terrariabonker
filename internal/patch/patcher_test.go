package patch_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
Applying and removing a patch leaves the same bytes behind, for every patch.

This is the part that writes instructions into a running game. Nothing here
asserts what the Go produced: a whole game is planted, both implementations
enable the same cheat over their own copy of it, and the *entire buffer* is
compared -- which catches a jump written to the wrong address as surely as a wrong
instruction, and catches a stub put in the wrong slot at all.

Then it is disabled, and the buffer has to be back exactly as it started. A
disable that does not restore is worse than an enable that never worked: the
method is left neither patched nor original, and it runs until it does not.
*/

// The planted game. The arena is the region the listing declares read-write-
// execute, stamped so it is adopted rather than allocated.
const (
	gameArena = arenaReal
	playerAt  = 0x10002800
	/*
		A second player copy, and a second site for the one anchor that is meant
		to match structural twins.

		Both exist because "write to every copy" is the rule this layer is built
		on and it is silent when broken: with one of each, a patcher that touched
		only the first would pass every test here.
	*/
	otherPlayerAt = 0x10003800
	otherTrydrop  = 0x10003000
)

/*
anchorTwins is a second match for some of the anchors.

mono can JIT one method into more than one arena, so a patch is applied to every
copy it finds -- and that rule is silent when broken. One twin is needed for each
*way* a patch writes, or a patcher that touched only the first copy would pass:
reset_block for the fixed in-place cheats, place for a tunable one, trydrop for
the injection that deliberately matches twins, and equip_benefits for an edit
that rides along with an injection.
*/
var anchorTwins = map[string]uint32{
	"reset_block":    0x10003400,
	"place":          0x10000100,
	"equip_benefits": 0x10000200,
	"trydrop":        otherTrydrop,
}

/*
anchorSites is where each anchor's match is planted. Every patch resolves through
one of these.

They are packed into 0x10004000..0x10008000 on purpose: that is the one
read-write-execute mapping in the listing below the arena, and a scan only looks
at executable memory. An anchor planted a little past the end of it simply is not
found -- which is right, and looks exactly like a method the game has not
compiled yet.
*/
var anchorSites = map[string]uint32{
	"reset_block":      0x10004000,
	"pylon_place":      0x10004380,
	"place":            0x10004700,
	"reset_minions":    0x10004A80,
	"getranges":        0x10004E00,
	"grabitems":        0x10005180,
	"get_spawn_rate":   0x10005500,
	"trydrop":          0x10005880,
	"equip_apply":      0x10005C00,
	"smart_cursor":     0x10005F80,
	"inventory_scan":   0x10006300,
	"grabitems_call":   0x10006680,
	"borders_movement": 0x10006A00,
	"trigger_ping":     0x10006D80,
	"player_teleport":  0x10007100,
	"equip_benefits":   0x10007480,
	"pick_tile":        0x10007800,
}

// game is a planted process: mappings, code, a player and an arena.
type game struct{ *planted }

/*
plantGame builds a game every patch can be applied to.

Each anchor is planted with its wildcards filled in, and then the original bytes
are written at every patch site *on top of that*. Both halves matter. An anchor
deliberately wildcards the bytes its own cheat overwrites, so that it still
resolves once the cheat is applied -- which means filling the wildcards alone
leaves a game whose patch sites hold nothing recognisable, and the guard that
refuses to write over unexpected bytes correctly refuses the lot. A game nobody
has patched has the real instructions there.
*/
func plantGame(t *testing.T) *game {
	t.Helper()
	mem := newMapped()
	for anchor, at := range anchorSites {
		mem.PokeBytes(at, filled(patch.Anchors[anchor].Pattern))
	}

	plantUnpatched(anchorSites, func(at uint32, b []byte) { mem.PokeBytes(at, b) })

	// get_LocalPlayer and the live player it leads to.
	asm := append([]byte{0x8B, 0x05}, u32(atPlayerStatic)...)
	asm = append(asm, 0x8B, 0x0D)
	asm = append(asm, u32(atMyPlayerStat)...)
	asm = append(asm, localPlayerTail...)
	mem.PokeBytes(atLocalPlayer, asm)
	mem.PokeBytes(atPlayerStatic, u32(atPlayerArray))
	mem.PokeI32(atMyPlayerStat, 0)
	mem.PokeBytes(atPlayerArray+0x10, u32(playerAt-locate.StatLifeFromObj))

	mem.PlantMonoString(0x10000040, "Nakama")
	mem.PlantPlayer(playerAt, []int32{500, 500, 137, 200, 220, 220}, 0x10000040)
	mem.PlantPlayer(otherPlayerAt, []int32{500, 500, 300, 200, 220, 220}, 0x10000040)
	for anchor, at := range anchorTwins {
		mem.PokeBytes(at, filled(patch.Anchors[anchor].Pattern))
	}
	plantUnpatched(anchorTwins, func(at uint32, b []byte) { mem.PokeBytes(at, b) })

	/*
		An arena of this program's, stamped, so it is adopted and nothing is
		allocated: the bootstrap needs a game that is running frames.

		Zeroed first, because that is how it arrives -- the allocation hands back
		zeroed pages, and the guard that refuses to write a stub over a live one
		accepts a slot only when it is zeros or scrubbed. A buffer left full of
		anything else is a slot that looks occupied.
	*/
	mem.PokeBytes(gameArena, make([]byte, patch.ArenaSize))
	mem.PokeBytes(gameArena+patch.ArenaMagicOff, patch.ArenaMagic)
	return &game{&planted{mem}}
}

func (g *game) Regions() []proc.Region { return g.AllRegions() }

/*
plantUnpatched writes the original instructions at every patch site.

Each is a place some cheat overwrites, which is exactly why the anchor that finds
it wildcards those bytes -- so they have to be put there separately for the game
to look like one nobody has touched.
*/
func plantUnpatched(sites map[string]uint32, poke func(uint32, []byte)) {
	at := func(anchor string) (uint32, bool) {
		a, ok := sites[anchor]
		return a, ok
	}
	for _, c := range patch.Cheats {
		if a, ok := at(c.Anchor); ok {
			poke(offsetOf(a, c.PatchOff), c.Orig)
		}
	}
	for _, inj := range patch.Injections {
		if a, ok := at(inj.Anchor); ok {
			poke(offsetOf(a, inj.InjectOff), inj.Overwrite)
		}
		for _, e := range inj.Edits {
			if a, ok := at(e.Anchor); ok {
				poke(offsetOf(a, e.Off), e.Orig)
			}
		}
	}
	for _, sb := range patch.Springboards {
		if a, ok := at(sb.Anchor); ok {
			poke(offsetOf(a, sb.Off), sb.Expect)
		}
	}
}

// offsetOf moves an address by an offset that may be negative: an injection
// point can sit in front of the pattern that found it.
func offsetOf(addr uint32, off int) uint32 {
	return uint32(int64(addr) + int64(off)) //nolint:gosec // a planted address
}

// pyGame is the Python over the same planted game, with its state and profile
// pointed somewhere disposable.
func pyGame(home string) string {
	plant := "SITES = {}\nTWINS = {}\ndef poke(at, b): mem.poke_bytes(at, b)\n"
	for anchor, at := range anchorSites {
		plant += fmt.Sprintf("SITES[%q] = %d\n", anchor, at)
	}
	for anchor, at := range anchorSites {
		plant += fmt.Sprintf(`
pat = P.ANCHORS[%q].pattern
mem.poke_bytes(%d, bytes((b if m else 0xCC) for b, m in zip(pat.raw, pat.mask)))`, anchor, at)
	}
	plant += "\n"
	for anchor, at := range anchorTwins {
		plant += fmt.Sprintf("TWINS[%q] = %d\n", anchor, at)
	}
	plant += `
for anchor, at in TWINS.items():
    pat = P.ANCHORS[anchor].pattern
    poke(at, bytes((b if m else 0xCC) for b, m in zip(pat.raw, pat.mask)))
for SRC in (SITES, TWINS):
    for c in P.CHEATS.values():
        if c.anchor in SRC:
            poke(SRC[c.anchor] + c.patch_off, c.orig)
    for i in P.INJECTIONS.values():
        if i.anchor in SRC:
            poke(SRC[i.anchor] + i.inject_off, i.overwrite)
        for e in i.edits:
            if e.anchor in SRC:
                poke(SRC[e.anchor] + e.off, e.orig)
    for key, off, expect in P.Patcher.SPRINGBOARDS:
        if key in SRC:
            poke(SRC[key] + off, expect)
`
	return pyMapped(plant+fmt.Sprintf(`
import struct
from terrariabonker import locate as L
code = (b"\x8b\x05" + struct.pack("<I", %d) + b"\x8b\x0d" + struct.pack("<I", %d)
        + L._LOCALPLAYER_TAIL)
mem.poke_bytes(%d, code)
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d, 0)
mem.poke_bytes(%d + 0x10, struct.pack("<I", %d))
mem.plant_mono_string(0x10000040, "Nakama")
mem.plant_player(%d, [500, 500, 137, 200, 220, 220], 0x10000040)
mem.plant_player(%d, [500, 500, 300, 200, 220, 220], 0x10000040)

mem.poke_bytes(%d, b"\x00" * P.Patcher.ARENA_SIZE)
mem.poke_bytes(%d + P.Patcher.ARENA_MAGIC_OFF, P.Patcher.ARENA_MAGIC)
L._exec_regions = lambda m: [(0x10000000, 0x10030000)]`,
		atPlayerStatic, atMyPlayerStat, atLocalPlayer,
		atPlayerStatic, atPlayerArray, atMyPlayerStat,
		atPlayerArray, playerAt-0x738, playerAt, otherPlayerAt,
		gameArena, gameArena)) + fmt.Sprintf(`
import os
from terrariabonker import profile
P._STATE = os.path.join(%q, "patches.json")
profile._PATH = os.path.join(%q, "profile.json")
p = P.Patcher(mem)
p._arena = %d
`, home, home, gameArena)
}

// newPatcher is the Go side over the same game, with its record disposable.
func newPatcher(t *testing.T, mem *game) *patch.Patcher {
	t.Helper()
	p := patch.NewPatcher(mem, 4242)
	return p
}

/*
restoresExactly reports whether turning this patch off leaves the game byte for
byte as it was found.

A cheat that carries a player field writes an off value into it instead of
restoring what was there, and a stub's cave is scrubbed rather than put back. The
comparison against the Python covers both; this only says which of them the
stricter check applies to.
*/
func restoresExactly(name string) bool {
	if c, ok := patch.Cheats[name]; ok {
		return c.ValueOff == 0
	}
	// A scrubbed arena slot is 0xCC where it was zeros, so only the sites are
	// restored exactly. The buffer comparison above is what covers the rest.
	return false
}

// everyPatch is every name in the catalog, so none is left untested by being
// forgotten.
func everyPatch() []string {
	var out []string
	for _, info := range patch.Catalog() {
		out = append(out, info.Name)
	}
	return out
}

/*
Enabling writes the same bytes, and disabling puts back exactly what was there.

Run for every patch there is, rather than a sample: each one has its own anchor,
its own displaced bytes and its own stub, and a mistake in any of them is
specific to it.
*/
func TestEnablingAndDisablingMatchesThePython(t *testing.T) {
	for _, name := range everyPatch() {
		t.Run(name, func(t *testing.T) {
			home := atHome(t)

			var want struct {
				Enabled  string `json:"enabled"`
				Disabled string `json:"disabled"`
				On       bool   `json:"on"`
				OffAgain bool   `json:"off_again"`
			}
			askPython(t, pyGame(home)+fmt.Sprintf(`
p.enable(%q)
enabled, on = mem.buf.hex(), p.is_enabled(%q)
p.disable(%q)
print(json.dumps({"enabled": enabled, "disabled": mem.buf.hex(),
                  "on": on, "off_again": p.is_enabled(%q)}))`, name, name, name, name), &want)

			mem := plantGame(t)
			before := mem.Hex()
			p := newPatcher(t, mem)

			require.NoError(t, p.Enable(name, nil), "enabling was refused")
			sameMemory(t, want.Enabled, mem.Hex(), "enabling wrote different bytes")
			require.True(t, want.On, "the Python did not see its own patch applied")
			require.Equal(t, want.On, p.IsEnabled(name), "disagree about it being on")

			require.NoError(t, p.Disable(name), "disabling was refused")
			sameMemory(t, want.Disabled, mem.Hex(), "disabling left different bytes")
			require.Equal(t, want.OffAgain, p.IsEnabled(name), "disagree about it being off")
			require.False(t, want.OffAgain, "the Python still saw its own patch applied")

			/*
				And the code is as it was, where turning a patch off is meant to
				leave it so.

				Two patches are not: a stub's cave is scrubbed to 0xCC rather
				than to whatever it held, and a cheat that carries a player field
				writes its *off* value there rather than restoring what the field
				was. Both are deliberate, both match the Python byte for byte
				above, and neither is "put the game back".
			*/
			if restoresExactly(name) {
				sameMemory(t, before, mem.Hex(), "the game was not put back as it was")
			}
		})
	}
}

/*
Applying a patch twice is not applying it twice.

The window re-applies on a value change, and the second pass is writing over its
own jump: the anchor must not be resolved again, because some injection sites
overlap their own pattern and a pristine scan finds nothing once the jump is in
place.
*/
func TestReapplyingMatchesThePython(t *testing.T) {
	for _, name := range []string{"tool_reach", "pickup", "loot", "mining", "max_minions"} {
		t.Run(name, func(t *testing.T) {
			home := atHome(t)

			var want struct {
				Twice string `json:"twice"`
				Back  string `json:"back"`
			}
			askPython(t, pyGame(home)+fmt.Sprintf(`
p.enable(%q)
p.enable(%q, 40)
twice = mem.buf.hex()
p.disable(%q)
print(json.dumps({"twice": twice, "back": mem.buf.hex()}))`, name, name, name), &want)

			mem := plantGame(t)
			before := mem.Hex()
			p := newPatcher(t, mem)

			forty := 40.0
			require.NoError(t, p.Enable(name, nil))
			require.NoError(t, p.Enable(name, &forty), "re-applying was refused")
			sameMemory(t, want.Twice, mem.Hex(), "re-applying wrote different bytes")

			require.NoError(t, p.Disable(name))
			sameMemory(t, want.Back, mem.Hex(), "disabling after a re-apply differs")
			if restoresExactly(name) {
				sameMemory(t, before, mem.Hex(), "the game was not put back as it was")
			}
		})
	}
}

/*
A patch whose anchor is not there is refused, and nothing is written.

A stub installed around addresses that did not resolve is a jump to nowhere on
the next frame.
*/
func TestAPatchWithoutItsAnchorIsRefused(t *testing.T) {
	for _, name := range []string{"mining", "tool_reach", "teleport"} {
		t.Run(name, func(t *testing.T) {
			atHome(t)
			mem := &game{&planted{newMapped()}} // mapped, nothing planted
			before := mem.Hex()
			p := patch.NewPatcher(mem, 4242)

			require.Error(t, p.Enable(name, nil), "a patch was applied with nothing resolved")
			sameMemory(t, before, mem.Hex(), "something was written anyway")
			require.False(t, p.IsEnabled(name))
		})
	}
}

/*
A site whose bytes are not what was expected is refused.

The check costs one read. It caught nothing for a year and then caught a jump
written 0x15 bytes early into Player.Update, which killed the game on the next
frame.
*/
func TestASiteThatIsNotWhatItShouldBeIsRefused(t *testing.T) {
	atHome(t)
	mem := plantGame(t)
	p := newPatcher(t, mem)

	// Something else is at tool_reach's injection point.
	inj := patch.Injections["tool_reach"]
	site := anchorSites[inj.Anchor] + uint32(inj.InjectOff)
	mem.PokeBytes(site, []byte{0x90, 0x90, 0x90, 0x90, 0x90})
	before := mem.Hex()

	err := p.Enable("tool_reach", nil)
	require.Error(t, err, "a jump went over bytes that were not the site's")
	require.Contains(t, err.Error(), "refusing to patch")
	sameMemory(t, before, mem.Hex(), "something was written anyway")
}

/*
An arena slot that already holds a stub is refused.

Writing over a live stub does not fault. It redirects that site's jump into this
one, which runs the wrong instructions and returns to the wrong method, and the
game dies later somewhere unrelated.
*/
func TestAnOccupiedArenaSlotIsRefused(t *testing.T) {
	atHome(t)
	mem := plantGame(t)
	p := newPatcher(t, mem)

	slot, err := patch.SlotFor(gameArena, "tool_reach", 0)
	require.NoError(t, err)
	mem.PokeBytes(slot, []byte{0x60, 0x6A, 0x40, 0x68}) // somebody's live code
	before := mem.Hex()

	err = p.Enable("tool_reach", nil)
	require.Error(t, err, "a stub was written over one that may be live")
	require.Contains(t, err.Error(), "Refusing to write over a stub")
	sameMemory(t, before, mem.Hex(), "something was written anyway")
}

/*
Every cheat that carries a player field writes it to every copy.

Which copy the game reads is not knowable here, and the inert ones ignore what
lands on them -- the same rule the service layer follows.
*/
func TestTheValueFieldsMatchThePython(t *testing.T) {
	for _, name := range []string{"mining", "reach"} {
		t.Run(name, func(t *testing.T) {
			home := atHome(t)

			var want struct {
				Default string `json:"default"`
				Chosen  string `json:"chosen"`
				Off     string `json:"off"`
			}
			askPython(t, pyGame(home)+fmt.Sprintf(`
p.enable(%q)
d = mem.buf.hex()
p.disable(%q)
p.enable(%q, 0.5)
c = mem.buf.hex()
p.disable(%q)
print(json.dumps({"default": d, "chosen": c, "off": mem.buf.hex()}))`,
				name, name, name, name), &want)

			mem := plantGame(t)
			p := newPatcher(t, mem)
			half := 0.5

			require.NoError(t, p.Enable(name, nil))
			sameMemory(t, want.Default, mem.Hex(), "the default value differs")
			require.NoError(t, p.Disable(name))
			require.NoError(t, p.Enable(name, &half))
			sameMemory(t, want.Chosen, mem.Hex(), "a chosen value differs")
			require.NoError(t, p.Disable(name))
			sameMemory(t, want.Off, mem.Hex(), "turning it off differs")
		})
	}
}

// The record written by a toggle is the record the Python writes.
func TestTheRecordAfterATogglesMatchesThePython(t *testing.T) {
	home := atHome(t)

	var want map[string]any
	askPython(t, pyGame(home)+`
p.enable("tool_reach", 55)
p.enable("mining")
print(json.dumps({"enabled": sorted(p._enabled), "values": p._values,
                  "inj": p._inj, "arena": p._arena}))`, &want)

	mem := plantGame(t)
	p := newPatcher(t, mem)
	fifty5 := 55.0
	require.NoError(t, p.Enable("tool_reach", &fifty5))
	require.NoError(t, p.Enable("mining", nil))

	got := patch.LoadState(4242)
	require.Equal(t, want["enabled"], asJSON(t, got.Enabled), "a different set is recorded on")
	require.Equal(t, want["values"], asJSON(t, got.Values), "different values are recorded")
	require.Equal(t, want["inj"], asJSON(t, got.Inj), "the stubs are recorded elsewhere")
	require.Equal(t, uint32(gameArena), got.Arena, "the arena was not recorded")
}

// Nothing in a scratch home leaks into the maintainer's own state.
func TestTheTestsNeverTouchTheRealState(t *testing.T) {
	home := atHome(t)
	require.Contains(t, patch.StatePath(), home,
		"the patch state would be written to the real home directory")
	_, err := os.Stat(home)
	require.NoError(t, err)
}
