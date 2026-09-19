package locate_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
Resolving the live player, or the trainer edits the wrong one.

A scan returns the live player and one or two inert copies, and writing to an
inert copy looks exactly like the trainer not working: the number changes in
memory and the game never reads it. This is the path that tells them apart --
the game's own get_LocalPlayer, found by its shape and followed to the object it
returns.
*/

// Where the pieces of the planted get_LocalPlayer go. They are spelled once and
// used from both languages so the two fakes are the same image.
const (
	lpBase           = 0x40000000
	lpSize           = 0x10000
	lpCode           = lpBase + 0x2000 // get_LocalPlayer
	lpArray          = lpBase + 0x4000 // the Main.player szarray
	lpObj            = lpBase + 0x5000 // the live Player object
	lpName           = lpBase + 0x60
	lpPlayerStatic   = lpBase + 0x100
	lpMyPlayerStatic = lpBase + 0x104
)

// plantLocalPlayer writes get_LocalPlayer, the two statics it reads, the player
// array and the player itself, and says which part of the buffer is code.
func plantLocalPlayer(mem *memtest.FakeMem, index int32, name string) uint32 {
	code := append([]byte{0x8B, 0x05}, word(lpPlayerStatic)...)
	code = append(code, 0x8B, 0x0D)
	code = append(code, word(lpMyPlayerStatic)...)
	code = append(code, localPlayerTailBytes...)
	mem.PokeBytes(lpCode, code)
	mem.Exec = []proc.Region{{Start: lpCode, End: lpCode + uint32(len(code)) + 16}} //nolint:gosec // a planted length

	mem.PokeBytes(lpPlayerStatic, word(lpArray))
	mem.PokeI32(lpMyPlayerStatic, index)
	life := uint32(lpObj + locate.StatLifeFromObj)
	mem.PokeBytes(lpArray+0x10+uint32(index)*4, word(lpObj)) //nolint:gosec // a planted index
	mem.PlantMonoString(lpName, name)
	mem.PlantPlayer(life, []int32{100, 100, 100, 20, 20, 20}, lpName)
	return life
}

/*
localPlayerTailBytes is the pattern, written out a second time.

A byte wrong in the package's own copy makes the scan find nothing, which looks
like a game update rather than a typo -- so the fixture does not reuse it. These
bytes were agreed with the implementation this was ported from.
*/
var localPlayerTailBytes = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}

func word(v uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, v)
}

// The pattern the scan looks for is the one this fixture plants.
func TestTheLocalPlayerPattern(t *testing.T) {
	require.Equal(t, localPlayerTailBytes, locate.LocalPlayerTail(),
		"the get_LocalPlayer pattern has changed")
}

/*
Player.statLife sits a fixed distance inside the Player object.

It is what turns the object address get_LocalPlayer returns into the address a
block is read from, so a wrong one resolves the right player and then reads
somebody else's numbers.
*/
func TestStatLifeFromObj(t *testing.T) {
	require.Equal(t, 1848, locate.StatLifeFromObj)
}

/*
Resolving through a planted get_LocalPlayer lands on the player it points at.

Run for a myPlayer of zero and for one that is not, because indexing the array
wrongly is invisible at index zero -- which is the index a test would otherwise
always use.
*/
func TestResolveLocalPlayer(t *testing.T) {
	for _, index := range []int32{0, 3} {
		t.Run(fmt.Sprintf("myPlayer=%d", index), func(t *testing.T) {
			mem := memtest.New(lpBase, lpSize)
			life := plantLocalPlayer(mem, index, "hero")

			anchor, ok := locate.FindLocalPlayerAnchor(mem)
			require.True(t, ok, "the anchor was not found")
			require.EqualValues(t, lpCode+0xC, anchor,
				"the anchor is not where the tail was planted")

			base, ok := locate.MainStaticBase(mem)
			require.True(t, ok, "Main's statics were not derived")
			require.EqualValues(t, lpPlayerStatic-layout.MainPlayerOff, base,
				"the base is not the static minus its offset")

			got, ok := locate.ResolveLocalPlayer(mem)
			require.True(t, ok, "no player was resolved")
			require.Equal(t, life, got.LifeAddr, "a different player was resolved")
			require.Equal(t, "hero", got.Name)
		})
	}
}

// With no get_LocalPlayer in the code nothing resolves, which is what sends the
// caller back to scanning and guessing.
func TestResolveLocalPlayerWithNoPattern(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	mem.Exec = []proc.Region{{Start: lpCode, End: lpCode + 0x1000}}

	_, ok := locate.ResolveLocalPlayer(mem)
	require.False(t, ok, "a player was resolved out of empty memory")
}

// Two copies of the pattern is not a tie to break: it gives up, so a game update
// that duplicates the shape cannot silently resolve the wrong array.
func TestASecondPatternMakesItGiveUp(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	plantLocalPlayer(mem, 0, "hero")
	second := uint32(lpCode + 0x400)
	code := append([]byte{0x8B, 0x05}, word(lpPlayerStatic)...)
	code = append(code, 0x8B, 0x0D)
	code = append(code, word(lpMyPlayerStatic)...)
	code = append(code, localPlayerTailBytes...)
	mem.PokeBytes(second, code)
	mem.Exec = []proc.Region{{Start: lpCode, End: second + uint32(len(code)) + 16}} //nolint:gosec // a planted length

	_, ok := locate.FindLocalPlayerAnchor(mem)
	require.False(t, ok, "one of two candidates was picked")
}

/*
A bare index-and-return tail with nothing loading statics in front of it is not
get_LocalPlayer, and is ignored.

That shape belongs to any array indexed and returned, so without the check on
the two `mov reg,[abs]` before it the scan finds several and resolves whichever
came first -- some other array, read as if it were the players.
*/
func TestATailWithoutTheStaticLoadsIsIgnored(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	plantLocalPlayer(mem, 0, "hero")
	bare := uint32(lpCode + 0x400)
	mem.PokeBytes(bare, localPlayerTailBytes)
	mem.Exec = []proc.Region{{Start: lpCode, End: bare + 0x100}}

	anchor, ok := locate.FindLocalPlayerAnchor(mem)
	require.True(t, ok, "a bare tail made the scan give up")
	require.EqualValues(t, lpCode+0xC, anchor, "the bare tail was chosen")
}

/*
A myPlayer the game cannot have means the anchor is stale, and it is refused.

Terraria indexes an array of 256 players. A larger index reads past the end of
it into whatever the heap put there, and taking that as the player would write
to an object that is not one.
*/
func TestAnImpossibleMyPlayerIsRefused(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	plantLocalPlayer(mem, 300, "hero")

	_, ok := locate.ResolveLocalPlayer(mem)
	require.False(t, ok, "an out-of-range myPlayer was trusted")
}
