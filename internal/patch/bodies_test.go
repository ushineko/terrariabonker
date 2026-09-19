package patch_test

import (
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

// goBuilder is the Go side over the same image.
func goBuilder(mem *planted) *patch.Builder {
	return &patch.Builder{Scanner: patch.NewScanner(mem), Mem: mem, Arena: stubArena}
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
