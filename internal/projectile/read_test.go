package projectile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
Reading what is in flight.

Everything the auto-catch decides rests on three floats in two arrays: whether a
bobber is reeling, whether something has bitten, and what bit. The fields sit
next to one that reads the same for every bobber in water -- `wet` was read as
`active` for eight releases, and every test agreed with it, because a bobber
floats.

So the fixture plants one of each case and this reads them back.
*/

// Finding the array in a planted game, and reading each planted bobber.
func TestReadingBobbers(t *testing.T) {
	mem := plant()

	found, ok := projectile.Array(mem, mainBase)
	require.True(t, ok, "the projectile array was not found")
	require.EqualValues(t, arr, found, "a different array was found")

	got := projectile.FindBobbers(mem, found)
	require.Len(t, got, 4, "a different number of live bobbers was found")

	bySlot := map[int]projectile.Bobber{}
	for _, b := range got {
		bySlot[b.Slot] = b
	}
	// The one that is cast and waiting: its counter climbs and nothing has bitten.
	waiting, there := bySlot[3]
	require.True(t, there, "the waiting bobber was not read")
	require.False(t, waiting.Reeling(), "a waiting bobber reads as being reeled in")
	require.False(t, waiting.Biting(), "a waiting bobber reads as having a bite")
	require.EqualValues(t, 412, waiting.Counter())

	// A fish on the line, which the catch slot spells as an item type.
	fish := bySlot[7]
	require.True(t, fish.Biting(), "a fish on the line does not read as a bite")
	require.EqualValues(t, 2290, fish.Catch())

	// And an NPC, which it spells negative.
	npc := bySlot[9]
	require.True(t, npc.Biting())
	require.EqualValues(t, -58, npc.Catch(), "an NPC on the line is not spelled negative")

	// Already being reeled in, so not biting however the rest reads.
	reeling := bySlot[11]
	require.True(t, reeling.Reeling())
	require.False(t, reeling.Biting(), "a bobber already being reeled read as a fresh bite")

	// The finished one is inactive, and the one that is not a bobber is not one.
	require.NotContains(t, bySlot, 13, "an inactive projectile was read as a live bobber")
	require.NotContains(t, bySlot, 17, "something that is not a bobber was read as one")
	require.NotContains(t, bySlot, 19, "a bobber whose floats do not read was read anyway")
}

/*
The bite the catcher acts on is the first one that is really biting.

One bobber is being reeled in already and one is finished; picking either would
arm the stub for a fish that is not there, which costs a press and looks like
the cheat missing.
*/
func TestFindBite(t *testing.T) {
	mem := plant()
	found, ok := projectile.Array(mem, mainBase)
	require.True(t, ok)

	bite, biting := projectile.FindBite(mem, found)
	require.True(t, biting, "no bite was found in a game with two on the line")
	require.Contains(t, []int{7, 9}, bite.Slot, "a bobber that is not biting was chosen")
	require.True(t, bite.Biting())
}

// One slot read on its own refuses a bobber that has finished, for the same
// reason.
func TestReadingOneSlot(t *testing.T) {
	mem := plant()
	found, ok := projectile.Array(mem, mainBase)
	require.True(t, ok)

	_, live := projectile.Read(mem, found, 7)
	require.True(t, live, "a live bobber did not read")

	_, gone := projectile.Read(mem, found, 13)
	require.False(t, gone, "a finished bobber read as a live one")
}

// An array that is not there is reported rather than read from zero.
func TestNoArrayInAnEmptyGame(t *testing.T) {
	_, ok := projectile.Array(memtest.New(base, size), mainBase)
	require.False(t, ok, "an array was found in empty memory")
}
