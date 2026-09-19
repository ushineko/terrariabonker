package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Which world is loaded, and why the name is what says so.

This used to be keyed on the tile buffer's address and the world's dimensions,
and that is byte-identical across a switch between two worlds of the same size --
measured, not supposed. The name carries the identity; the dimensions only
corroborate it.
*/
func TestWorldIDMatchesThePython(t *testing.T) {
	var want any
	askPython(t, preamble()+plantWorld("Nakama's World")+`
got = svc.world_id()
print(json.dumps(None if got is None else list(got)))`, &want)
	require.NotNil(t, want, "the Python could not identify a world it was given")

	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	got, ok := service.New(mem, -1).WorldID()
	require.True(t, ok, "the world was not identified")

	fields := want.([]any)
	require.Equal(t, fields[0], got.Name, "a different world name")
	require.Equal(t, fields[1], asJSON(t, got.Width), "a different width")
	require.Equal(t, fields[2], asJSON(t, got.Height), "a different height")
}

/*
Two worlds of the same size are told apart.

The dimensions and the tile buffer are identical across that switch, so anything
that keyed on them would call these one world and skip the re-apply that a world
change is supposed to trigger.
*/
func TestTwoWorldsOfTheSameSizeAreDifferentWorlds(t *testing.T) {
	first := plant()
	plantWorldInto(first, "Nakama's World")
	a, ok := service.New(first, -1).WorldID()
	require.True(t, ok)

	second := plant()
	plantWorldInto(second, "Somewhere Else")
	b, ok := service.New(second, -1).WorldID()
	require.True(t, ok)

	require.Equal(t, a.Width, b.Width, "the fixture does not plant two worlds of one size")
	require.Equal(t, a.Height, b.Height)
	require.NotEqual(t, a, b, "two worlds of the same size were called the same world")
}

// With no world loaded, nothing is claimed rather than something guessed.
func TestNoWorldLoaded(t *testing.T) {
	svc := service.New(plant(), -1) // the fixture plants no tile buffer

	_, err := svc.TileMap()
	require.Error(t, err, "a tile map was built with no world")

	_, ok := svc.WorldID()
	require.False(t, ok, "a world was identified with none loaded")
}

/*
A world whose name will not read is not a world.

The dimensions are still perfectly readable in that case, so anything that fell
back to them would report a world -- and then fail to notice the next switch to
one of the same size.
*/
func TestAWorldWithNoReadableNameIsNotOne(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	svc := service.New(mem, -1)

	_, ok := svc.WorldID()
	require.True(t, ok, "the fixture does not plant a readable world")

	// The name pointer goes, and nothing else does: the tiles still read.
	mem.PokeBytes(staticAt+layout.MainWorldNameOff, make([]byte, 4))
	_, err := svc.TileMap()
	require.NoError(t, err, "the tiles stopped reading, so this proves nothing")

	_, ok = svc.WorldID()
	require.False(t, ok, "a world with no readable name was reported as one")
}

/*
Main's statics are found once and shared.

Finding them is a full scan of every executable mapping. The world identity is
read on every status poll, and paying for that scan each time would be the whole
budget -- which is invisible to every other test here, because the answers stay
right and only the cost moves.
*/
func TestTheStaticBaseIsFoundOnce(t *testing.T) {
	counted := &countingMem{execMem: plant()}
	plantWorldInto(counted.execMem, "Nakama's World")
	svc := service.New(counted, -1)

	_, ok := svc.WorldID()
	require.True(t, ok)
	require.NotZero(t, counted.scans, "the first lookup did not scan, so this proves nothing")

	counted.scans = 0
	for range 5 {
		_, ok = svc.WorldID()
		require.True(t, ok)
	}
	require.Zerof(t, counted.scans,
		"five more lookups scanned %d times: the statics are being found again each call",
		counted.scans)
}

/*
A base that stops describing a world is dropped, so the next call finds the real
one.

That is what a world reload looks like from here. Keeping the stale base means
every later call fails against an address that no longer means anything, and
nothing ever recovers short of restarting the trainer.
*/
func TestAStaleStaticBaseIsDropped(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	svc := service.New(mem, -1)

	_, err := svc.TileMap()
	require.NoError(t, err, "the fixture does not plant a readable world")

	// The world goes, and comes back somewhere else: the statics moved, and
	// get_LocalPlayer now leads to the new block.
	mem.PokeBytes(staticAt+layout.MainTileOff, make([]byte, 4))
	_, err = svc.TileMap()
	require.Error(t, err, "a world that is gone still read")

	plantWorldAt(mem, movedStaticAt, "Nakama's World")
	mem.PokeBytes(playerArray+layout.ArrDataOff+myPlayer*4, u32(liveObj))

	_, err = svc.TileMap()
	require.NoError(t, err, "the stale base was kept, so nothing recovered")

	got, ok := svc.WorldID()
	require.True(t, ok)
	require.Equal(t, "Nakama's World", got.Name)
}
