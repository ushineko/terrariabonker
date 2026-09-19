package patch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
The words each stub reads out of the arena, and the order they are written in.

The ordering is the part that matters. The extractor's count is written *last*,
so the stub can never see a count covering coordinates that are only half
written -- it would mine whatever happened to be there, and mining the wrong tile
cannot be undone. Auto-use's flag is consumed before the press, so a stub that
dies between the two presses nothing rather than pressing forever.
*/

// The stub's own tally is read back, not this side's idea of it.
func TestThePressCountIsTheStubsOwn(t *testing.T) {
	const arena = arenaReal
	mem := newMapped()
	mem.PokeBytes(arena+patch.ArenaMagicOff, patch.ArenaMagic)
	p := patch.NewPatcher(&planted{mem}, 4242)

	// As though the stub had run seven times.
	mem.PokeI32(arena+patch.AutoUseCountOff, 7)
	require.Equal(t, int32(7), p.AutoUse().Presses(),
		"the count came from somewhere other than the arena")

	// Arming does not touch it: a caller that armed and reads back fewer knows
	// the presses did not happen.
	p.AutoUse().Arm()
	require.Equal(t, int32(7), p.AutoUse().Presses(), "arming moved the stub's own tally")
}

/*
The count is written after the coordinates.

Checked by watching the writes rather than the result: with the count first, a
stub reading between the two writes sees a count covering coordinates that are
still whatever was there before.
*/
func TestTheQueueCountIsWrittenLast(t *testing.T) {
	const arena = arenaReal
	mem := newMapped()
	mem.PokeBytes(arena+patch.ArenaMagicOff, patch.ArenaMagic)
	watched := &watcher{planted: &planted{mem}}
	p := patch.NewPatcher(watched, 4242)

	require.Equal(t, 2, p.OreQueue().Arm([]patch.Tile{{X: 5, Y: 6}, {X: 7, Y: 8}}))

	count := uint32(arena + patch.OreQueueOff)
	pairs := count + 4
	var sawPairs bool
	for _, w := range watched.writes {
		if w == pairs {
			sawPairs = true
		}
		if w == count {
			require.True(t, sawPairs,
				"the count was written before the coordinates it covers")
			return
		}
	}
	require.Fail(t, "the count was never written")
}

// With no arena there is nothing to arm, and saying so beats writing to nowhere.
func TestTheArenaWordsWithoutAnArena(t *testing.T) {
	p := patch.NewPatcher(&planted{newMapped()}, 4242) // nothing stamped

	require.False(t, p.AutoUse().Arm(), "a press was armed with no arena")
	require.False(t, p.AutoUse().Armed())
	require.Zero(t, p.AutoUse().Presses())
	require.False(t, p.AutoUse().Disarm())

	_, ok := p.OreQueue().Address()
	require.False(t, ok, "the queue claimed an address with no arena")
	require.Zero(t, p.OreQueue().Arm([]patch.Tile{{X: 1, Y: 2}}), "tiles were queued to nowhere")
	require.False(t, p.OreQueue().Armed())
	require.False(t, p.OreQueue().Disarm())
}

/*
No cheat's words overlap another's.

They are all offsets into one 64 KB block, and auto-use's arm flag was once
inside the extractor's queue: mining a vein wrote the tile count into the arm
word and the stub pressed the use button for every batch queued. Nothing in the
auto-use code was involved, which is what made it baffling in the log.
*/
func TestTheArenaWordsDoNotOverlap(t *testing.T) {
	spans := []struct {
		name     string
		lo, size int
	}{
		{"ore queue", patch.OreQueueOff, patch.OreQueueSize},
		{"auto-use armed", patch.AutoUseArmedOff, 4},
		{"auto-use count", patch.AutoUseCountOff, 4},
		{"auto-use release", patch.AutoUseReleaseOff, 4},
	}
	for i, a := range spans {
		require.Lessf(t, a.lo+a.size, patch.ArenaStubsOff,
			"%s runs into the stub slots", a.name)
		for _, b := range spans[i+1:] {
			require.Falsef(t, a.lo < b.lo+b.size && b.lo < a.lo+a.size,
				"%s overlaps %s", a.name, b.name)
		}
	}
}

// manyTiles is a queue longer than the batch holds.
func manyTiles(n int) []patch.Tile {
	out := make([]patch.Tile, n)
	for i := range out {
		out[i] = patch.Tile{X: int32(1000 + i), Y: 240} //nolint:gosec // a small count
	}
	return out
}
