package memtest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The fake memory both languages build must be the same memory.

This is the gate spec 051 puts in front of every module that touches a process:
the Python suite tests its locator against a bytearray with structures planted
in it, and the Go one cannot be trusted against a different bytearray. So the
same plants are made on both sides and the buffers compared byte for byte.

What is really being pinned is not the fake -- it is the layout. Where a mono
string keeps its length, that the length counts characters rather than bytes,
how far below a player's life its name pointer sits. Those are facts about the
game, and a fixture that plants them wrongly agrees with a reader that reads
them wrongly.

So the bytes are frozen. They are what the implementation this was ported from
planted, while both existed.
*/

/*
A planted player is the same bytes on both sides.

The block is the game's own order and the name pointer sits a long way below the
life field, because the life field is what a scan finds first and everything
else is reached from it.
*/
func TestAPlantedPlayerIsTheSameBytes(t *testing.T) {
	const base, size = 0x10000000, 0x400
	const life = base + 0x200
	const namePtr = base + 0x40

	got := memtest.New(base, size)
	got.PlantPlayer(life, []int32{7, 11, 400, 400, 200, 200}, namePtr)
	got.PlantMonoString(namePtr, "Nakama")

	require.Equal(t, "6ad85295755614d3", digest(got.Buf),
		"a player is planted differently; if that was deliberate, update the digest")
}

/*
A mono string's length is in characters, not bytes.

Getting that wrong reads a name of five characters as ten and walks into
whatever follows it, which is the kind of mistake that looks like a corrupt save
rather than a bug.
*/
func TestAMonoStringCountsCharacters(t *testing.T) {
	const base, size = 0x10000000, 0x400

	for name, want := range map[string]string{
		"Nakama":     "6dd849ae532e67e8",
		"":           "b00f58e4c719ed9e",
		"a":          "54fac0a341391977",
		"Zoë":        "cbd054202ef84679",
		"player one": "bc3fa7833beabfcc",
	} {
		got := memtest.New(base, size)
		got.PlantMonoString(base+0x40, name)
		require.Equalf(t, want, digest(got.Buf), "%q is planted differently", name)
	}
}

/*
A read or a write outside the mapping fails rather than panicking.

A real process has gaps between its regions, and a locator walking one has to
cope with running off the end. A fake that panicked there would let a walker
that cannot cope pass its tests.
*/
func TestReadingOutsideTheMappingFails(t *testing.T) {
	const base, size = 0x10000000, 0x100
	mem := memtest.New(base, size)

	require.Nil(t, mem.Read(base-4, 4), "below the region")
	require.Nil(t, mem.Read(base+size-2, 4), "off the end of it")
	require.False(t, mem.Write(base+size, []byte{1, 2, 3, 4}))

	_, ok := mem.ReadU32(base + size)
	require.False(t, ok)

	mem.PokeI32(base, -1)
	value, ok := mem.ReadU32(base)
	require.True(t, ok)
	require.Equal(t, uint32(0xFFFFFFFF), value, "a signed word is written little-endian")

	require.Equal(t, []proc.Region{{Start: base, End: base + size}}, mem.Regions())
}

// digest is a buffer as one short string, for freezing one without writing a
// kilobyte of hex into this file.
func digest(buf []byte) string {
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:8])
}
