package patch_test

import (
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

// Nothing in a scratch home leaks into the maintainer's own state.
func TestTheTestsNeverTouchTheRealState(t *testing.T) {
	home := atHome(t)
	require.Contains(t, patch.StatePath(), home,
		"the patch state would be written to the real home directory")
	_, err := os.Stat(home)
	require.NoError(t, err)
}
