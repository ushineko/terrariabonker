package memtest_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
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
game, and they are about to exist in two languages.
*/

const pythonTimeout = 2 * time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// pythonBuf builds the same fake in Python and returns its buffer.
func pythonBuf(t *testing.T, script string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)

	var encoded string
	require.NoError(t, json.Unmarshal(out, &encoded))
	raw, err := hex.DecodeString(encoded)
	require.NoError(t, err)
	return raw
}

// preamble builds the Python fake the suite's conftest defines.
const preamble = `
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
mem = FakeMem(0x10000000, 0x400)
`

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

	want := pythonBuf(t, preamble+`
mem.plant_player(0x10000200, [7, 11, 400, 400, 200, 200], 0x10000040)
mem.plant_mono_string(0x10000040, "Nakama")
print(json.dumps(mem.buf.hex()))
`)

	require.Equal(t, hex.EncodeToString(want), hex.EncodeToString(got.Buf),
		"the two fakes are not the same memory")
}

/*
A mono string's length is in characters, not bytes.

Getting that wrong reads a name of five characters as ten and walks into
whatever follows it, which is the kind of mistake that looks like a corrupt save
rather than a bug.
*/
func TestAMonoStringCountsCharacters(t *testing.T) {
	const base, size = 0x10000000, 0x400

	for _, name := range []string{"Nakama", "", "a", "Zoë", "player one"} {
		got := memtest.New(base, size)
		got.PlantMonoString(base+0x40, name)

		want := pythonBuf(t, preamble+`
mem.plant_mono_string(0x10000040, `+quote(name)+`)
print(json.dumps(mem.buf.hex()))
`)
		require.Equalf(t, hex.EncodeToString(want), hex.EncodeToString(got.Buf),
			"%q is planted differently", name)
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

	require.Equal(t, [][2]uint32{{base, base + size}}, mem.Regions())
}

// quote is a Python string literal, so a name with anything awkward in it
// survives the trip.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
