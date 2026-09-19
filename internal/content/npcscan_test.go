package content_test

import (
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

// With nothing plausible anywhere, both report nothing rather than reading a
// table out of noise.
func TestNoNPCArrayAtAll(t *testing.T) {
	mem := memtest.New(scanBase, scanSize)
	_, ok := content.FindNPCArray(mem, staticBase)
	require.False(t, ok, "an array was found in empty memory")
	require.Empty(t, content.FrameCounts(mem, staticBase), "frame counts were read from nothing")
}
