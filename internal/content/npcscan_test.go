package content_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
Finding the NPC array, and what makes something not it.

Every offset involved is a build constant, so each is checked rather than trusted
and each has a fallback that looks for the thing by its shape. What matters is
that both implementations reject the same near misses: an array of the right
length whose elements are unrelated, and a table of ones that is some other
table.
*/

const (
	scanBase   = 0x10000000
	scanSize   = 0x30000
	staticBase = scanBase + 0x100

	npcArr   = scanBase + 0x5000 // the real one, at the pinned offset
	decoyArr = scanBase + 0x9000 // the right length, elements that share nothing
	npcElems = scanBase + 0xC000

	/*
		The real frame counts, and three tables that are not them.

		Each fails a different check, and each is met by the fallback scan before
		the real one: too short to cover the NPC types, a count larger than any
		sprite sheet has, and an array of ones, which is some other array
		entirely.
	*/
	frameArr = scanBase + 0x14000
	onesArr  = scanBase + 0x18000
	shortArr = scanBase + 0x1A000
	hugeArr  = scanBase + 0x1E000
)

/*
plantNPCs builds a static block with the arrays in it.

pinned says whether the pinned offsets point at the real arrays. With them
pointing elsewhere, both implementations fall back to scanning the block -- which
is the path that exists so a rebuild costs a scan rather than a cheat.
*/
func plantNPCs(pinned bool) *memtest.FakeMem {
	mem := memtest.New(scanBase, scanSize)

	// The real array: the right length, and every element behind one vtable.
	mem.PokeI32(npcArr+0x0C, 201) // MaxNPCs + 1
	for i := range 201 {
		elem := uint32(npcElems + i*0x40) //nolint:gosec // a planted address
		mem.PokeBytes(npcArr+0x10+uint32(i)*4, u32(elem))
		mem.PokeBytes(elem, u32(vtable))
	}

	// A decoy of the same length whose elements share nothing.
	mem.PokeI32(decoyArr+0x0C, 201)
	for i := range 201 {
		elem := uint32(scanBase + 0x1C000 + i*0x10) //nolint:gosec // a planted address
		mem.PokeBytes(decoyArr+0x10+uint32(i)*4, u32(elem))
		mem.PokeBytes(elem, u32(uint32(0xAAAA0000+i))) //nolint:gosec // a planted vtable
	}

	// The frame counts, and a table of ones beside them.
	mem.PokeI32(frameArr+0x0C, 700)
	for i := range 700 {
		mem.PokeI32(frameArr+0x10+uint32(i)*4, int32(i%5)) //nolint:gosec // a planted count
	}
	mem.PokeI32(onesArr+0x0C, 700)
	for i := range 700 {
		mem.PokeI32(onesArr+0x10+uint32(i)*4, 1)
	}
	mem.PokeI32(shortArr+0x0C, 100)
	for i := range 100 {
		mem.PokeI32(shortArr+0x10+uint32(i)*4, int32(i%5)) //nolint:gosec // a planted count
	}
	mem.PokeI32(hugeArr+0x0C, 700)
	for i := range 700 {
		mem.PokeI32(hugeArr+0x10+uint32(i)*4, int32(100+i%5)) //nolint:gosec // a planted count
	}

	// The pinned slots. The decoy is placed *before* the real array in the block
	// so that a fallback scan meets it first and has to reject it.
	mem.PokeBytes(staticBase+0x9B0, u32(pick(pinned, npcArr, 0)))   // Main.npc
	mem.PokeBytes(staticBase+0xC34, u32(pick(pinned, frameArr, 0))) // Main.npcFrameCount
	mem.PokeBytes(staticBase+0x1000, u32(decoyArr))
	mem.PokeBytes(staticBase+0x1004, u32(shortArr))
	mem.PokeBytes(staticBase+0x1008, u32(hugeArr))
	mem.PokeBytes(staticBase+0x100C, u32(onesArr))
	mem.PokeBytes(staticBase+0x1010, u32(npcArr))
	mem.PokeBytes(staticBase+0x1014, u32(frameArr))
	return mem
}

func pick(yes bool, a, b uint32) uint32 {
	if yes {
		return a
	}
	return b
}

// pyPlantNPCs is the same static block as Python source.
func pyPlantNPCs(pinned bool) string {
	return fmt.Sprintf(`
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import npcs, locate
mem = FakeMem(%d, %d)
mem.poke_i32(%d + 0x0C, 201)
for i in range(201):
    elem = %d + i * 0x40
    mem.poke_bytes(%d + 0x10 + i * 4, struct.pack("<I", elem))
    mem.poke_bytes(elem, struct.pack("<I", %d))
mem.poke_i32(%d + 0x0C, 201)
for i in range(201):
    elem = %d + i * 0x10
    mem.poke_bytes(%d + 0x10 + i * 4, struct.pack("<I", elem))
    mem.poke_bytes(elem, struct.pack("<I", 0xAAAA0000 + i))
mem.poke_i32(%d + 0x0C, 700)
for i in range(700):
    mem.poke_i32(%d + 0x10 + i * 4, i %% 5)
mem.poke_i32(%d + 0x0C, 700)
for i in range(700):
    mem.poke_i32(%d + 0x10 + i * 4, 1)
mem.poke_i32(%d + 0x0C, 100)
for i in range(100):
    mem.poke_i32(%d + 0x10 + i * 4, i %% 5)
mem.poke_i32(%d + 0x0C, 700)
for i in range(700):
    mem.poke_i32(%d + 0x10 + i * 4, 100 + i %% 5)
mem.poke_bytes(%d + 0x9B0, struct.pack("<I", %d))
mem.poke_bytes(%d + 0xC34, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x1000, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x1004, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x1008, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x100C, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x1010, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x1014, struct.pack("<I", %d))
locate.main_static_base = lambda m: %d
`, scanBase, scanSize,
		npcArr, npcElems, npcArr, vtable,
		decoyArr, scanBase+0x1C000, decoyArr,
		frameArr, frameArr, onesArr, onesArr,
		shortArr, shortArr, hugeArr, hugeArr,
		staticBase, pick(pinned, npcArr, 0),
		staticBase, pick(pinned, frameArr, 0),
		staticBase, decoyArr, staticBase, shortArr,
		staticBase, hugeArr, staticBase, onesArr,
		staticBase, npcArr, staticBase, frameArr,
		staticBase)
}

// The array is found the same way, whether the pinned offset holds or not.
func TestFindingTheNPCArrayMatchesThePython(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			var want map[string]any
			askPython(t, pyPlantNPCs(pinned)+`
arr = npcs.find_npc_array(mem)
print(json.dumps({"arr": arr, "vtable": npcs.find_npc_vtable(mem)}))`, &want)

			mem := plantNPCs(pinned)
			arr, ok := content.FindNPCArray(mem, staticBase)
			require.True(t, ok, "the array was not found")
			require.Equal(t, want["arr"], asJSON(t, arr), "a different array was found")
			require.Equal(t, uint32(npcArr), arr, "and it is not the one that was planted")

			vt, ok := content.FindNPCVTable(mem, staticBase)
			require.True(t, ok)
			require.Equal(t, want["vtable"], asJSON(t, vt), "a different vtable")
		})
	}
}

/*
An array of the right length whose elements share nothing is rejected.

The game allocates every slot at world load, so the elements really do share a
vtable; requiring that agreement is what tells the array from anything else of
the same length.
*/
func TestAnArrayWhoseElementsShareNothingIsRejected(t *testing.T) {
	mem := plantNPCs(false)
	_, ok := content.NPCVTableOf(mem, decoyArr)
	require.False(t, ok, "an array of unrelated elements was taken for the NPCs")

	_, ok = content.NPCVTableOf(mem, npcArr)
	require.True(t, ok, "the real array was rejected")
}

// The frame counts are found the same way, and a table of ones is not them.
func TestFrameCountsMatchThePython(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			var want map[string]int32
			askPython(t, pyPlantNPCs(pinned)+`
print(json.dumps({str(k): v for k, v in npcs.read_frame_counts(mem).items()}))`, &want)
			require.NotEmpty(t, want, "the Python found no frame counts")

			got := content.FrameCounts(plantNPCs(pinned), staticBase)
			require.Len(t, got, len(want), "a different number of types has frames")
			for key, v := range want {
				var id int
				_, err := fmt.Sscanf(key, "%d", &id)
				require.NoError(t, err)
				require.Equalf(t, v, got[id], "type %s has a different frame count", key)
			}
			// Zero-frame types are left out rather than recorded as zero.
			require.NotContains(t, got, 0, "a type with no frames was recorded")
		})
	}
}

// With nothing plausible anywhere, both report nothing rather than reading a
// table out of noise.
func TestNoNPCArrayAtAll(t *testing.T) {
	mem := memtest.New(scanBase, scanSize)
	_, ok := content.FindNPCArray(mem, staticBase)
	require.False(t, ok, "an array was found in empty memory")
	require.Empty(t, content.FrameCounts(mem, staticBase), "frame counts were read from nothing")
}
