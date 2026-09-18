package locate_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The live player is resolved the same way in both languages, or the trainer edits
the wrong one.

find_players returns the live player and one or two inert copies, and writing to
an inert copy looks exactly like the trainer not working: the number changes in
memory and the game never reads it. This is the path that tells them apart, so
the Python is asked what it resolved and the Go has to land on the same address.
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

// lpPreamble builds the Python's fake at the same base and size, and plants the
// same get_LocalPlayer into it.
const lpPreamble = `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import locate as L
mem = FakeMem(0x40000000, 0x10000)
code = (b"\x8b\x05" + struct.pack("<I", 0x40000100)
        + b"\x8b\x0d" + struct.pack("<I", 0x40000104)
        + L._LOCALPLAYER_TAIL)
`

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

// localPlayerTailBytes is the pattern as the Go package holds it, fetched from
// the Python so the two copies cannot drift apart unnoticed. A byte wrong here
// makes the scan find nothing, which looks like a game update rather than a
// typo.
var localPlayerTailBytes = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}

func word(v uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, v)
}

// The pattern itself is the Python's, byte for byte.
func TestTheLocalPlayerPatternMatchesThePython(t *testing.T) {
	var want []int
	askPython(t, lpPreamble+`print(json.dumps(list(L._LOCALPLAYER_TAIL)))`, &want)

	got := make([]int, len(localPlayerTailBytes))
	for i, b := range localPlayerTailBytes {
		got[i] = int(b)
	}
	require.Equal(t, want, got, "the get_LocalPlayer pattern differs between the two")
}

// The offset of Player.statLife inside the Player object is the Python's too:
// it is what turns an object address into the address a block is read from.
func TestStatLifeFromObjMatchesThePython(t *testing.T) {
	var want uint32
	askPython(t, lpPreamble+`print(json.dumps(L.STATLIFE_FROM_OBJ))`, &want)
	require.Equal(t, want, uint32(locate.StatLifeFromObj))
}

// Resolving through a planted get_LocalPlayer lands on the same player, for a
// myPlayer of zero and for one that is not, because indexing the array wrongly
// is invisible at index zero.
func TestResolveLocalPlayerMatchesThePython(t *testing.T) {
	for _, index := range []int32{0, 3} {
		t.Run(fmt.Sprintf("myPlayer=%d", index), func(t *testing.T) {
			mem := memtest.New(lpBase, lpSize)
			life := plantLocalPlayer(mem, index, "hero")

			var want map[string]any
			askPython(t, lpPreamble+fmt.Sprintf(`
mem.write(0x%X, code)
mem.write(0x%X, struct.pack("<I", 0x%X))
mem.write(0x%X, struct.pack("<i", %d))
mem.write(0x%X + 0x10 + %d * 4, struct.pack("<I", 0x%X))
mem.plant_mono_string(0x%X, "hero")
mem.plant_player(0x%X + L.STATLIFE_FROM_OBJ, [100, 100, 100, 20, 20, 20], 0x%X)
L._exec_regions = lambda m: [(0x%X, 0x%X + len(code) + 16)]
lp = L.resolve_local_player(mem)
print(json.dumps(None if lp is None else {
    "addr": lp.life_addr, "name": lp.name,
    "anchor": L.find_localplayer_anchor(mem),
    "base": L.main_static_base(mem),
}))
`, lpCode, lpPlayerStatic, lpArray, lpMyPlayerStatic, index,
				lpArray, index, lpObj, lpName, lpObj, lpName, lpCode, lpCode), &want)

			require.NotNil(t, want, "the Python resolved nobody from a planted get_LocalPlayer")

			anchor, ok := locate.FindLocalPlayerAnchor(mem)
			require.True(t, ok, "the anchor is there for the Python and not here")
			require.Equal(t, uint32(want["anchor"].(float64)), anchor, "a different anchor")

			base, ok := locate.MainStaticBase(mem)
			require.True(t, ok)
			require.Equal(t, uint32(want["base"].(float64)), base, "a different Main static base")

			got, ok := locate.ResolveLocalPlayer(mem)
			require.True(t, ok, "the Python resolved a player and the Go did not")
			require.Equal(t, uint32(want["addr"].(float64)), got.LifeAddr, "a different player")
			require.Equal(t, life, got.LifeAddr, "and not the one that was planted")
			require.Equal(t, want["name"], got.Name)
		})
	}
}

// With no get_LocalPlayer in the code, both report nothing, which is what sends
// the caller back to scanning and guessing.
func TestResolveLocalPlayerAgreesWhenThePatternIsGone(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	mem.Exec = []proc.Region{{Start: lpCode, End: lpCode + 0x1000}}

	var want any
	askPython(t, lpPreamble+`
L._exec_regions = lambda m: [(0x40002000, 0x40003000)]
print(json.dumps(L.resolve_local_player(mem)))
`, &want)
	require.Nil(t, want, "the Python found a player in empty memory")

	_, ok := locate.ResolveLocalPlayer(mem)
	require.False(t, ok, "the Go found a player in empty memory")
}

// Two copies of the pattern is not a tie to break: both give up, so a game
// update that duplicates the shape cannot silently resolve the wrong array.
func TestASecondPatternMakesBothGiveUp(t *testing.T) {
	mem := memtest.New(lpBase, lpSize)
	plantLocalPlayer(mem, 0, "hero")
	second := uint32(lpCode + 0x400)
	code := append([]byte{0x8B, 0x05}, word(lpPlayerStatic)...)
	code = append(code, 0x8B, 0x0D)
	code = append(code, word(lpMyPlayerStatic)...)
	code = append(code, localPlayerTailBytes...)
	mem.PokeBytes(second, code)
	mem.Exec = []proc.Region{{Start: lpCode, End: second + uint32(len(code)) + 16}} //nolint:gosec // a planted length

	var want any
	askPython(t, lpPreamble+fmt.Sprintf(`
mem.write(0x%X, code)
mem.write(0x%X, code)
L._exec_regions = lambda m: [(0x%X, 0x%X + len(code) + 16)]
print(json.dumps(L.find_localplayer_anchor(mem)))
`, lpCode, second, lpCode, second), &want)
	require.Nil(t, want, "the Python picked one of two candidates")

	_, ok := locate.FindLocalPlayerAnchor(mem)
	require.False(t, ok, "the Go picked one of two candidates")
}

/*
A bare index-and-return tail with nothing loading statics in front of it is not
get_LocalPlayer, and both ignore it.

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

	var want any
	askPython(t, lpPreamble+fmt.Sprintf(`
mem.write(0x%X, code)
mem.write(0x%X, struct.pack("<I", 0x%X))
mem.write(0x%X, struct.pack("<i", 0))
mem.write(0x%X + 0x10, struct.pack("<I", 0x%X))
mem.write(0x%X, L._LOCALPLAYER_TAIL)
L._exec_regions = lambda m: [(0x%X, 0x%X)]
print(json.dumps(L.find_localplayer_anchor(mem)))
`, lpCode, lpPlayerStatic, lpArray, lpMyPlayerStatic, lpArray, lpObj,
		bare, lpCode, bare+0x100), &want)
	require.NotNil(t, want, "the Python gave up over a bare tail")

	anchor, ok := locate.FindLocalPlayerAnchor(mem)
	require.True(t, ok, "the Go gave up over a bare tail")
	require.Equal(t, uint32(want.(float64)), anchor, "a different anchor was chosen")
}

/*
A myPlayer the game cannot have means the anchor is stale, and both refuse it.

Terraria indexes an array of 256 players. A larger index reads past the end of
it into whatever the heap put there, and taking that as the player would write
to an object that is not one.
*/
func TestAnImpossibleMyPlayerIsRefused(t *testing.T) {
	const index = 300
	mem := memtest.New(lpBase, lpSize)
	plantLocalPlayer(mem, index, "hero")

	var want any
	askPython(t, lpPreamble+fmt.Sprintf(`
mem.write(0x%X, code)
mem.write(0x%X, struct.pack("<I", 0x%X))
mem.write(0x%X, struct.pack("<i", %d))
mem.write(0x%X + 0x10 + %d * 4, struct.pack("<I", 0x%X))
mem.plant_mono_string(0x%X, "hero")
mem.plant_player(0x%X + L.STATLIFE_FROM_OBJ, [100, 100, 100, 20, 20, 20], 0x%X)
L._exec_regions = lambda m: [(0x%X, 0x%X + len(code) + 16)]
print(json.dumps(L.resolve_local_player(mem)))
`, lpCode, lpPlayerStatic, lpArray, lpMyPlayerStatic, index,
		lpArray, index, lpObj, lpName, lpObj, lpName, lpCode, lpCode), &want)
	require.Nil(t, want, "the Python trusted an out-of-range myPlayer")

	_, ok := locate.ResolveLocalPlayer(mem)
	require.False(t, ok, "the Go trusted an out-of-range myPlayer")
}
