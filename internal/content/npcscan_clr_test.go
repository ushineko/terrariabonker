package content_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
The NPC scan under .NET Framework (spec 052).

Every number planted here is a literal from the measurement, not read from
internal/layout: cmd/clrfields against 1.4.5.8 on .NET Framework 4.8.1,
2026-10-08 (docs/clr-fields-1.4.5.8.txt).

  - An szarray is a MethodTable, the length at +4, the elements from +8.
  - Main.npc's static slot is 0x54 below Main.player's (0x824 against 0x878) and
    Main.npcFrameCount's is 0xA0 above it (0x918).
  - An NPC keeps type at 0x0F4, damage 0x108, defense 0x10C, lifeMax 0x120, netID
    0x14C, boss (a bool) 0x1D1, townNPC (a bool) 0x1D6 and color (four bytes)
    0x208; width and height are Entity's, at 0x010 and 0x014.
*/

const (
	clrPlayerSlot = scanBase + 0x900
	clrNPCSlot    = clrPlayerSlot - 0x54
	clrFrameSlot  = clrPlayerSlot + 0xA0
	clrNPCMT      = 0x084FE6F0
)

func netfx(t *testing.T) layout.Entry {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return e
}

/*
plantCLRNPCs builds Main.npc and Main.npcFrameCount the CLR way, reached from
Main.player's slot. length is how long the NPC array says it is, and shared is
whether its elements carry one MethodTable.
*/
func plantCLRNPCs(length int32, shared bool) *memtest.FakeMem {
	mem := memtest.New(scanBase, scanSize)
	mem.PokeBytes(clrNPCSlot, u32(npcArr))
	mem.PokeI32(npcArr+0x04, length)
	for i := range 201 {
		elem := uint32(npcElems + i*0x40) //nolint:gosec // a planted address
		mem.PokeBytes(npcArr+0x08+uint32(i)*4, u32(elem))
		mt := uint32(clrNPCMT)
		if !shared {
			mt = uint32(0xAAAA0000 + i) //nolint:gosec // a planted MethodTable
		}
		mem.PokeBytes(elem, u32(mt))
	}
	mem.PokeBytes(clrFrameSlot, u32(frameArr))
	mem.PokeI32(frameArr+0x04, 700)
	for i := range 700 {
		mem.PokeI32(frameArr+0x08+uint32(i)*4, int32(i%5)) //nolint:gosec // a planted count
	}
	return mem
}

// The array is the one Main.npc's slot holds, and its elements' MethodTable is
// what the template scan looks for.
func TestTheCLRNPCArrayIsFoundThroughThePlayerSlot(t *testing.T) {
	mem := plantCLRNPCs(201, true)
	arr, ok := content.FindNPCArray(mem, netfx(t), clrPlayerSlot)
	require.True(t, ok, "the NPC array was not found")
	require.Equal(t, uint32(npcArr), arr)

	mt, ok := content.FindNPCVTable(mem, netfx(t), clrPlayerSlot)
	require.True(t, ok)
	require.Equal(t, uint32(clrNPCMT), mt)
}

/*
A slot that does not hold the NPC array is no answer.

There is no block to scan instead under the CLR, so each check is the whole
defence: the game's own length, and elements that share a MethodTable.
*/
func TestACLRSlotThatIsNotTheNPCArrayIsRejected(t *testing.T) {
	for _, length := range []int32{0, 200, 202, 1001} {
		_, ok := content.FindNPCArray(plantCLRNPCs(length, true), netfx(t), clrPlayerSlot)
		require.Falsef(t, ok, "an array of %d was taken for the NPCs", length)
	}
	_, ok := content.FindNPCArray(plantCLRNPCs(201, false), netfx(t), clrPlayerSlot)
	require.False(t, ok, "an array of unrelated elements was taken for the NPCs")

	// From the wrong slot, nothing: the distances are from Main.player's.
	_, ok = content.FindNPCArray(plantCLRNPCs(201, true), netfx(t), clrPlayerSlot+4)
	require.False(t, ok, "the array was found from a slot that is not Main.player's")
}

// An entry with no resolver has no NPC array, whatever is in memory.
func TestAnEntryWithNoNPCResolverFindsNothing(t *testing.T) {
	e := netfx(t)
	e.NPC.Resolver = ""
	_, ok := content.FindNPCArray(plantCLRNPCs(201, true), e, clrPlayerSlot)
	require.False(t, ok)
	require.Empty(t, content.FrameCounts(plantCLRNPCs(201, true), e, clrPlayerSlot))
}

// The frame counts are read through Main.npcFrameCount's slot, zeros left out
// as they are under mono.
func TestTheCLRFrameCounts(t *testing.T) {
	got := content.FrameCounts(plantCLRNPCs(201, true), netfx(t), clrPlayerSlot)
	require.Len(t, got, 700-140, "a count of zero is not a sheet")
	require.Equal(t, int32(3), got[3])
	require.Equal(t, int32(4), got[699])
	require.NotContains(t, got, 5)

	// A table of ones in the slot is some other array.
	mem := plantCLRNPCs(201, true)
	for i := range 700 {
		mem.PokeI32(frameArr+0x08+uint32(i)*4, 1) //nolint:gosec // a planted count
	}
	require.Empty(t, content.FrameCounts(mem, netfx(t), clrPlayerSlot))
}

// The mono block's pinned offsets lead to the same arrays the fallback scan
// finds past the decoys in front of them.
func TestTheMonoNPCArrayPinnedAndByScan(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		mem := plantNPCs(pinned)
		arr, ok := content.FindNPCArray(mem, layout.Mono(), staticBase)
		require.Truef(t, ok, "pinned=%v: no array", pinned)
		require.Equalf(t, uint32(npcArr), arr, "pinned=%v", pinned)

		got := content.FrameCounts(mem, layout.Mono(), staticBase)
		require.Lenf(t, got, 700-140, "pinned=%v", pinned)
		require.Equalf(t, int32(4), got[699], "pinned=%v", pinned)
	}
}

/*
An NPC template read at the CLR offsets carries what was planted at them.

Each field is given a value of its own, so one read from a neighbour's offset
shows up as the wrong number rather than as a coincidence.
*/
func TestFindingNPCTemplatesUnderTheCLR(t *testing.T) {
	mem := memtest.New(base, size)
	plantOne := func(at uint32, netID, npcType, life int32) {
		mem.PokeBytes(at, u32(clrNPCMT))
		mem.PokeI32(at+0x0F4, npcType)
		mem.PokeI32(at+0x14C, netID)
		mem.PokeI32(at+0x120, life)
	}
	plantOne(base+0x10000, 1, 1, 25)
	plantOne(base+0x10300, -3, 1, 14)
	plantOne(base+0x10600, 4, 4, 2800)
	plantOne(base+0x10900, 17, 17, 250)
	// The boss, with every field set.
	boss := uint32(base + 0x10600)
	mem.PokeI32(boss+0x108, 15)  // damage
	mem.PokeI32(boss+0x10C, 12)  // defense
	mem.PokeI32(boss+0x010, 100) // width
	mem.PokeI32(boss+0x014, 110) // height
	mem.PokeBytes(boss+0x1D1, []byte{1})
	mem.PokeBytes(boss+0x208, []byte{10, 20, 30, 40})
	mem.PokeBytes(uint32(base+0x10900)+0x1D6, []byte{1}) // a town NPC

	got := content.FindNPCTemplates(mem, clrNPCMT, nil, netfx(t).NPC)
	require.Len(t, got, 4)
	require.Equal(t, content.NPCStats{Type: 4, NetID: 4, Life: 2800, Damage: 15, Defense: 12,
		Width: 100, Height: 110, Boss: true, Color: [4]byte{10, 20, 30, 40}}, got[4])
	require.Equal(t, content.NPCStats{Type: 1, NetID: -3, Life: 14}, got[-3],
		"a variant is keyed by its negative net id and names its own type")
	require.True(t, got[17].Town)
	require.False(t, got[17].Boss)

	// An object too close to the end of memory to hold a whole NPC is not read.
	edge := memtest.New(base, 0x1000)
	edge.PokeBytes(base+0x1000-0x200, u32(clrNPCMT))
	require.Empty(t, content.FindNPCTemplates(edge, clrNPCMT, nil, netfx(t).NPC))
}
