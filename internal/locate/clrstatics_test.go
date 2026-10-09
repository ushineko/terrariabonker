package locate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

const sBase = 0x10000000

// plantCLRWorld is memtest's CLR world, shared with the service's tests.
func plantCLRWorld(t *testing.T) memtest.CLRWorld {
	t.Helper()
	return memtest.PlantCLRWorld(sBase)
}

func TestTheCLRLivePlayerIsTheActiveElementOfMainPlayer(t *testing.T) {
	w := plantCLRWorld(t)
	loc := clrLocator(t)

	copies := loc.FindPlayers(w.Mem)
	require.Len(t, copies, 2, "the live player and the snapshot")
	require.EqualValues(t, w.Snap+memtest.CLRLifeInPlayer, copies[0].LifeAddr,
		"the snapshot is not first, so this does not test picking past it")

	slot, ok := loc.FindPlayerSlot(w.Mem, copies)
	require.True(t, ok)
	require.EqualValues(t, w.Slot, slot)

	live, ok := loc.LiveAt(w.Mem, slot)
	require.True(t, ok)
	require.EqualValues(t, w.Live+memtest.CLRLifeInPlayer, live.LifeAddr, "the snapshot was taken for the live player")
	require.Equal(t, "terrariabonker", live.Name)
}

// A slot that holds Main.player's array but has nothing of the right length
// beside it is not Main's block: one array alone is a coincidence.
func TestASlotWithoutTheOtherTwoArraysIsNotMains(t *testing.T) {
	w := plantCLRWorld(t)
	decoy := uint32(sBase + 0x12000)
	w.Mem.PlantCLRStatics(decoy, w.Players, 0, 0)

	slot, ok := clrLocator(t).FindPlayerSlot(w.Mem, clrLocator(t).FindPlayers(w.Mem))
	require.True(t, ok)
	require.EqualValues(t, w.Slot, slot, "the decoy was taken for Main's block")
}

// Two slots that both look exactly like Main's block are no answer.
func TestTwoBlocksThatBothFitAreNoAnswer(t *testing.T) {
	w := plantCLRWorld(t)
	w.Mem.PlantCLRStatics(sBase+0x14878, w.Players, w.NPCs, w.Projectiles)

	_, ok := clrLocator(t).FindPlayerSlot(w.Mem, clrLocator(t).FindPlayers(w.Mem))
	require.False(t, ok, "a tie was broken")
}

/*
The slot is kept; what it points to is re-read. The GC moves the Player[] and the
players in it, and the slot then leads to the new array.
*/
func TestTheSlotFollowsTheArrayWhenItMoves(t *testing.T) {
	w := plantCLRWorld(t)
	loc := clrLocator(t)
	slot, ok := loc.FindPlayerSlot(w.Mem, loc.FindPlayers(w.Mem))
	require.True(t, ok)

	moved := uint32(sBase + 0xC000)
	w.Mem.PlantCLRArray(moved, memtest.CLRPlayerSlots, w.Elements(w.Live))
	w.Mem.PlantCLRArray(w.Players, 0, nil) // the old array is gone
	w.Mem.PlantCLRStatics(w.Slot, moved, w.NPCs, w.Projectiles)

	live, ok := loc.LiveAt(w.Mem, slot)
	require.True(t, ok)
	require.EqualValues(t, w.Live+memtest.CLRLifeInPlayer, live.LifeAddr)
}

/*
Two active players is not single-player, and is no answer.

The second is a real player -- the snapshot, put into slot 1 -- so a reader that
took either one would get a valid block back, and only refusing passes.
*/
func TestTwoActivePlayersAreNoAnswer(t *testing.T) {
	w := plantCLRWorld(t)
	loc := clrLocator(t)
	slot, ok := loc.FindPlayerSlot(w.Mem, loc.FindPlayers(w.Mem))
	require.True(t, ok)

	elems := w.Elements(w.Live)
	elems[1] = w.Snap
	w.Mem.PlantCLRArray(w.Players, memtest.CLRPlayerSlots, elems)
	_, ok = loc.LiveAt(w.Mem, slot)
	require.False(t, ok)
}

// The mono locator has no statics to find: it uses the JIT anchor instead.
func TestTheMonoLocatorFindsNoStaticsSlot(t *testing.T) {
	w := plantCLRWorld(t)
	_, ok := locate.With(monoEntry(t)).FindPlayerSlot(w.Mem, clrLocator(t).FindPlayers(w.Mem))
	require.False(t, ok)
}

/*
A kept slot that has stopped reading as Main's block is not used.

Static storage does not move, but a slot kept across a world unload, or one that
was never Main's, must not lead anywhere: LiveAt re-checks the three arrays every
time. Here Main.npc stops pointing at an NPC[201]; the player array beside it is
untouched and would still yield the live player to a reader that did not look.
*/
func TestASlotThatStoppedBeingMainsIsNotUsed(t *testing.T) {
	w := plantCLRWorld(t)
	loc := clrLocator(t)
	slot, ok := loc.FindPlayerSlot(w.Mem, loc.FindPlayers(w.Mem))
	require.True(t, ok)

	w.Mem.PlantCLRStatics(w.Slot, w.Players, 0, w.Projectiles)
	_, ok = loc.LiveAt(w.Mem, slot)
	require.False(t, ok, "a slot that no longer reads as Main's led to a player")
}
