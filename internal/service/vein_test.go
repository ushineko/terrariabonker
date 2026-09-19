package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
What a vein miner would take, and the two rules that stop it taking more.

A flood matches on the starting tile's own id, so a copper vein touching an iron
one takes the copper. And a deposit that has *moved* -- silt and slush fall when
what is under them goes -- is re-found by walking straight down its own columns
and stopping at the first ground that is not the vein. Searching a box instead
found unrelated deposits of the same ore below and mined those too, which is what
"it mines non-contiguous sections across the screen" turned out to be.
*/

// The vein the world fixture plants sits at these coordinates.
const veinX, veinY = 10, 12

// plantVeinInto puts an ore vein in the planted world, with a second deposit of
// the same ore well below it and ground in between.
func plantVeinInto(mem *execMem) {
	// Stone everywhere in the band, so the columns have ground in them.
	for x := int32(veinX - 4); x <= veinX+4; x++ {
		for y := int32(veinY - 2); y <= veinY+40; y++ {
			plantTile(mem, x, y, 1, true)
		}
	}
	// The vein itself: copper, four tiles.
	for _, p := range [][2]int32{{veinX, veinY}, {veinX + 1, veinY}, {veinX, veinY + 1}, {veinX + 1, veinY + 1}} {
		plantTile(mem, p[0], p[1], 7, true)
	}
	// An iron tile touching it, which a flood must not take.
	plantTile(mem, veinX+2, veinY, 6, true)
	// And an unrelated copper deposit a long way below, under the ground.
	plantTile(mem, veinX, veinY+30, 7, true)
}

/*
plantTallVein is a vein taller than one batch, with an empty shaft beneath it.

Falling is only observable across batches: a vein that fits in one is queued
once, and whether the search re-finds it is never asked.
*/
const tallVeinTiles = 40

func plantTallVein(mem *execMem) {
	// A shaft of open space for the vein to fall into, and ground under that.
	for x := int32(veinX); x <= veinX+1; x++ {
		for y := int32(veinY); y < veinY+40; y++ {
			plantTile(mem, x, y, 0, false)
		}
		for y := int32(veinY + 40); y < veinY+45; y++ {
			plantTile(mem, x, y, 1, true)
		}
	}
	for x := int32(veinX); x <= veinX+1; x++ {
		for y := int32(veinY); y < veinY+tallVeinTiles/2; y++ {
			plantTile(mem, x, y, 7, true)
		}
	}
}

// A vein is one ore: the iron touching the copper is not part of it.
func TestAVeinStopsAtAnotherOre(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)

	got, err := service.New(mem, -1).VeinAt(veinX, veinY, false, 0, true)
	require.NoError(t, err)
	require.Len(t, got.Tiles, 4, "the flood did not take the whole copper vein")
	for _, p := range got.Tiles {
		require.NotEqual(t, [2]int32{veinX + 2, veinY}, p, "the flood took the iron beside it")
		require.NotEqual(t, [2]int32{veinX, veinY + 30}, p,
			"the flood took an unrelated deposit below the ground")
	}
}

// The extractor refuses to run when its cheat is not applied, rather than
// queueing tiles nothing will mine.
func TestExtractingWithoutTheCheat(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)

	p := patchFor(t, mem)
	_, err := service.New(mem, -1).ExtractVein(p, veinX, veinY, false, 0, 0)
	require.Error(t, err, "the extractor ran with its cheat off")
	require.Contains(t, err.Error(), "not enabled")
}

// Nothing is queued for a tile nobody may take.
func TestExtractingSomethingThatIsNotAVein(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)
	p := enabledPatcher(t, mem)

	got, err := service.New(mem, -1).ExtractVein(p, veinX, veinY+5, false, 0, 0)
	require.NoError(t, err)
	require.Zero(t, got.Queued, "tiles were queued for plain stone")
	require.Equal(t, "not a whitelisted tile", got.Reason)
}

/*
A deposit that has fallen is found again in its own column, and the search stops
at ground.

Silt and slush fall when what is under them goes, so between batches the first
flood's coordinates are stale -- the blocks are lower down. Gravity is vertical,
so searching a box instead finds unrelated deposits of the same ore below and
mines those, which is ore nobody asked for.
*/
func TestTheRegrowthSearchStopsAtGround(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)
	p := enabledPatcher(t, mem)

	// The vein is gone, and the unrelated copper below the ground is still there.
	for _, q := range [][2]int32{{veinX, veinY}, {veinX + 1, veinY}, {veinX, veinY + 1}, {veinX + 1, veinY + 1}} {
		plantTile(mem, q[0], q[1], 0, false)
	}

	got, err := service.New(mem, -1).ExtractVein(p, veinX, veinY, false, 0, 0)
	require.NoError(t, err)
	require.Zero(t, got.Queued, "a vein that is gone was still queued")

	// And the deposit below is untouched: it is under stone, which the search
	// stops at.
	tm, err := service.New(mem, -1).TileMap()
	require.NoError(t, err)
	id, there := tm.SolidTypeAt(veinX, veinY+30)
	require.True(t, there, "the unrelated deposit below the ground was mined")
	require.Equal(t, uint16(7), id)
}

/*
A vein is mined a batch at a time, and re-found between batches.

Nothing in a planted game breaks a tile, so this gives it a stub that does: it
watches for the queue's count being written, which is the last thing an arm does,
and clears exactly the tiles it names.
*/
func TestExtractingMinesTheWholeVein(t *testing.T) {
	game, p := miningGame(t, false)
	plantVeinInto(game.execMem)

	got, err := service.New(game, -1).ExtractVein(p, veinX, veinY, false, 0, time.Second)
	require.NoError(t, err)
	require.Equal(t, 4, got.Queued, "a different vein was found")
	require.Equal(t, 4, got.Mined, "the vein was not mined")
	require.Zero(t, got.Left, "something was left behind")
	require.Empty(t, got.Reason, "the run stopped early: %s", got.Reason)

	tm, err := service.New(game, -1).TileMap()
	require.NoError(t, err)
	for _, q := range [][2]int32{{veinX, veinY}, {veinX + 1, veinY}, {veinX, veinY + 1}, {veinX + 1, veinY + 1}} {
		_, there := tm.SolidTypeAt(q[0], q[1])
		require.Falsef(t, there, "%v was not mined", q)
	}
	// And the iron beside it, and the deposit below the ground, are untouched.
	id, there := tm.SolidTypeAt(veinX+2, veinY)
	require.True(t, there, "the iron beside the vein was mined")
	require.Equal(t, uint16(6), id)
	_, there = tm.SolidTypeAt(veinX, veinY+30)
	require.True(t, there, "the unrelated deposit below the ground was mined")
}

/*
A deposit that falls is followed down its own column, and no further.

Silt and slush drop when what is under them goes, so the first flood's
coordinates are stale between batches. Searching a box instead of a column found
unrelated deposits of the same ore below and mined those too.
*/
func TestAFallingDepositIsFollowedDownItsColumn(t *testing.T) {
	game, p := miningGame(t, true)
	// Taller than one batch, with a shaft under it to fall into: a vein that
	// fits in a single batch is never re-found, so nothing would be tested.
	plantTallVein(game.execMem)

	got, err := service.New(game, -1).ExtractVein(p, veinX, veinY, false, 0, time.Second)
	require.NoError(t, err)
	require.Equal(t, tallVeinTiles, got.Queued, "a different vein was found")
	require.Greater(t, game.armed, 1, "it all went in one batch, so nothing was re-found")
	require.Equal(t, tallVeinTiles, got.Mined, "a falling vein was not followed down")
}

/*
The backstop: never take more tiles than the vein that was asked for held.

Whatever the re-find turns up, a vein cannot grow. Without the cap, a vein that
falls onto a bigger deposit of the same ore would keep going into it.
*/
func TestNoMoreIsTakenThanTheVeinHeld(t *testing.T) {
	game, p := miningGame(t, true)
	plantVeinInto(game.execMem)
	// A much larger deposit of the same ore, directly beneath the vein, with no
	// ground between: exactly what a falling vein lands in.
	for y := int32(veinY + 2); y < veinY+20; y++ {
		plantTile(game.execMem, veinX, y, 7, true)
		plantTile(game.execMem, veinX+1, y, 7, true)
	}

	got, err := service.New(game, -1).ExtractVein(p, veinX, veinY, false, 0, time.Second)
	require.NoError(t, err)
	require.LessOrEqualf(t, got.Mined, got.Queued,
		"mined %d of a vein that held %d: the cap did not hold", got.Mined, got.Queued)
}

// A flood that hits its limit says so.
func TestACappedVeinSaysSo(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)
	svc := service.New(mem, -1)

	full, err := svc.VeinAt(veinX, veinY, false, 0, true)
	require.NoError(t, err)
	require.False(t, full.Capped, "a vein smaller than the limit was called capped")

	short, err := svc.VeinAt(veinX, veinY, false, 2, true)
	require.NoError(t, err)
	require.Len(t, short.Tiles, 2, "the limit was not applied")
	require.True(t, short.Capped, "a flood that stopped at its limit did not say so")
}

// A player standing left of the origin floors to the tile they are in, rather
// than truncating toward it.
func TestAPlayerTileFloors(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantPositionInto(mem, -24.0, -24.0)

	x, y, err := service.New(mem, -1).PlayerTile()
	require.NoError(t, err)
	require.Equal(t, int32(-2), x, "a negative position truncated instead of flooring")
	require.Equal(t, int32(-2), y)
}

/*
The queue is left empty, whatever happened.

A queue still holding tiles is re-mined on every frame, so a run that ended --
finished, stalled, or gave up -- must leave nothing in it.
*/
func TestTheQueueIsLeftEmpty(t *testing.T) {
	game, p := miningGame(t, false)
	plantVeinInto(game.execMem)

	_, err := service.New(game, -1).ExtractVein(p, veinX, veinY, false, 0, time.Second)
	require.NoError(t, err)
	require.False(t, p.OreQueue().Armed(), "the queue was left armed after a finished run")

	// And after one that stopped early, for a vein nothing will break.
	stuck, stuckP := miningGame(t, false)
	stuck.deaf = true
	plantVeinInto(stuck.execMem)
	got, err := service.New(stuck, -1).ExtractVein(stuckP, veinX, veinY, false, 0, 50*time.Millisecond)
	require.NoError(t, err)
	require.NotEmpty(t, got.Reason, "a run against a game that mines nothing did not stop early")
	require.False(t, stuckP.OreQueue().Armed(), "the queue was left armed after a stall")
}

// A batch is handed over in pieces, not all at once: a whole vein in one frame
// would run hundreds of tile breaks together.
func TestAVeinIsHandedOverInBatches(t *testing.T) {
	game, p := miningGame(t, false)
	plantTallVein(game.execMem)

	got, err := service.New(game, -1).ExtractVein(p, veinX, veinY, false, 0, time.Second)
	require.NoError(t, err)
	require.Equal(t, tallVeinTiles, got.Mined)
	require.Equalf(t, 2, game.armed,
		"%d tiles went over in %d batches: the cap of %d is not being applied",
		tallVeinTiles, game.armed, patch.OreMaxBatch)
}
