package patch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The stubs built from live state assemble to the same bytes.

These are the ones that cannot be constants: they bake resolved method entries,
the statics that lead to the live player, and this program's own arena. Every one
of those is an address written into an instruction, and an address written wrong
is a call into the middle of something.

So a game is planted with the anchors and call sites these need, and both
implementations build their stubs over it.
*/

// Where the planted anchors go, inside the executable region the listing
// declares. Far enough apart that no pattern can be found inside another.
const (
	atEquipApply    = 0x10000800
	atEquipBenefits = 0x10000C00
	atPickTile      = 0x10001000
	atLocalPlayer   = 0x10001400
	atPlayerStatic  = 0x10001800
	atMyPlayerStat  = 0x10001804
	atPlayerArray   = 0x10001900
	atPlayerObj     = 0x10002000
	stubArena       = 0x10018000 // the RWX region in the listing
)

// planted is the fake the stub builders are given.
type planted struct{ *mapped }

func (p *planted) Regions() []proc.Region { return p.AllRegions() }

/*
plantForStubs plants everything the three stubs resolve.

Wildcards are filled with a byte the patterns cannot be asking for, which
includes the call displacements: what those resolve to is arbitrary, and
arbitrary is the point -- both implementations have to arrive at the same
arbitrary address.
*/
func plantForStubs() *planted {
	mem := newMapped()
	for _, a := range []struct {
		anchor string
		at     uint32
	}{
		{"equip_apply", atEquipApply},
		{"equip_benefits", atEquipBenefits},
		{"pick_tile", atPickTile},
	} {
		mem.PokeBytes(a.at, filled(patch.Anchors[a.anchor].Pattern))
	}

	// get_LocalPlayer, and the player it leads to.
	asm := append([]byte{0x8B, 0x05}, u32(atPlayerStatic)...)
	asm = append(asm, 0x8B, 0x0D)
	asm = append(asm, u32(atMyPlayerStat)...)
	asm = append(asm, localPlayerTail...)
	mem.PokeBytes(atLocalPlayer, asm)
	mem.PokeBytes(atPlayerStatic, u32(atPlayerArray))
	mem.PokeI32(atMyPlayerStat, 0)
	mem.PokeBytes(atPlayerArray+0x10, u32(atPlayerObj))

	life := uint32(atPlayerObj + locate.StatLifeFromObj)
	mem.PlantMonoString(0x10000040, "Nakama")
	mem.PlantPlayer(life, []int32{500, 500, 137, 200, 220, 220}, 0x10000040)
	return &planted{mem}
}

// pyStubs is the Python with the same planting and a patcher over it.
func pyStubs() string {
	return pyMapped(fmt.Sprintf(`
for key, at in (("equip_apply", %d), ("equip_benefits", %d), ("pick_tile", %d)):
    pat = P.ANCHORS[key].pattern
    mem.poke_bytes(at, bytes((b if m else 0xCC) for b, m in zip(pat.raw, pat.mask)))
import struct
from terrariabonker import locate as L
code = (b"\x8b\x05" + struct.pack("<I", %d) + b"\x8b\x0d" + struct.pack("<I", %d)
        + L._LOCALPLAYER_TAIL)
mem.poke_bytes(%d, code)
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d, 0)
mem.poke_bytes(%d + 0x10, struct.pack("<I", %d))
mem.plant_mono_string(0x10000040, "Nakama")
mem.plant_player(%d + L.STATLIFE_FROM_OBJ, [500, 500, 137, 200, 220, 220], 0x10000040)
L._exec_regions = lambda m: [(0x10000000, 0x10030000)]`,
		atEquipApply, atEquipBenefits, atPickTile,
		atPlayerStatic, atMyPlayerStat, atLocalPlayer,
		atPlayerStatic, atPlayerArray, atMyPlayerStat,
		atPlayerArray, atPlayerObj, atPlayerObj)) + fmt.Sprintf(`
p._arena = %d
p.arena = lambda *a, **k: %d
`, stubArena, stubArena)
}

// goBuilder is the Go side over the same image.
func goBuilder(mem *planted) *patch.Builder {
	return &patch.Builder{Scanner: patch.NewScanner(mem), Mem: mem, Arena: stubArena}
}

// Where a call inside an anchor match goes is the same address.
func TestCallTargetsMatchThePython(t *testing.T) {
	cases := []struct {
		anchor string
		off    int
	}{
		{"equip_apply", 15},    // ApplyEquipFunctional
		{"equip_benefits", 20}, // GrantPrefixBenefits
		{"equip_benefits", 36}, // GrantArmorBenefits
	}

	var want []uint32
	askPython(t, pyStubs()+fmt.Sprintf(`
print(json.dumps([p._call_target(a, o) for a, o in %s]))`, pyCalls(cases)), &want)

	b := goBuilder(plantForStubs())
	for i, c := range cases {
		got, err := patch.CallTarget(b, c.anchor, c.off)
		require.NoErrorf(t, err, "%s+%d did not resolve", c.anchor, c.off)
		require.Equalf(t, want[i], got, "%s+%d calls somewhere else", c.anchor, c.off)
	}
}

// The three stubs built from live state assemble to the same bytes.
func TestTheBuiltStubsMatchThePython(t *testing.T) {
	for _, name := range []string{"inventory_accs", "ore_extract", "auto_use"} {
		t.Run(name, func(t *testing.T) {
			var want string
			askPython(t, pyStubs()+fmt.Sprintf(`
inj = P.INJECTIONS[%q]
print(json.dumps(inj.build_body(p, inj).hex()))`, name), &want)
			require.NotEmpty(t, want)

			mem := plantForStubs()
			inj := patch.Injections[name]
			got, err := inj.BuildBody(goBuilder(mem), inj)
			require.NoError(t, err)
			require.Equal(t, want, hexOf(got), "the stub assembles differently")

			// A stub has to fit the slot it is given, jump back included.
			require.LessOrEqual(t, len(got)+5, patch.ArenaSlot,
				"the stub no longer fits an arena slot")
		})
	}
}

/*
The managed-call stub is the same, and it carries the addresses it was given.

It is the only stub that calls back into the game's own code, so the entry it
bakes is the difference between a teleport and a jump into whatever is at that
address.
*/
func TestTheTeleportStubMatchesThePython(t *testing.T) {
	const playerBase, target = 0x0AC00000, 0x21001234

	var want string
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import patcher as P
print(json.dumps(P._teleport_body(%d, %d).hex()))`, playerBase, target), &want)

	require.Equal(t, want, hexOf(patch.TeleportBody(playerBase, target)))
}

/*
A stub whose anchors are not there is refused, not built from nothing.

A body assembled around a call target of zero installs cleanly and jumps to
address zero on the next frame.
*/
func TestAStubWithoutItsAnchorsIsRefused(t *testing.T) {
	mem := &planted{newMapped()} // mapped, and nothing planted in it
	b := goBuilder(mem)

	_, err := patch.CallTarget(b, "equip_apply", 15)
	require.Error(t, err, "a call target was produced with no anchor")

	for _, name := range []string{"inventory_accs", "ore_extract"} {
		inj := patch.Injections[name]
		_, err := inj.BuildBody(b, inj)
		require.Errorf(t, err, "%s built a stub with nothing resolved", name)
	}
}
