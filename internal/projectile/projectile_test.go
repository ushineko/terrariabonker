package projectile_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
The bobber, and the two flags that decide whether a line is in the water.

The array holds every projectile forever, and a finished one is marked inactive
with its old fields left exactly where they were -- the bobber flag included. A
reader that filtered on the bobber flag alone reported a line still out minutes
after it came in, which made auto-catch refuse to cast because it believed the
water was busy. So the fixture carries a bobber that has been reeled in, and the
comparison covers it.
*/

const (
	base = 0x10000000
	size = 0x20000

	mainBase = base + 0x100
	arr      = base + 0x2000
	decoy    = base + 0x8000
	// The right length and one shared vtable, but mostly empty: the game
	// allocates every slot up front and never leaves a hole, so a sparse array
	// is something else.
	sparse  = base + 0xC000
	objects = base + 0x10000
	floats  = base + 0x1000

	arrayLen = 1001
)

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

/*
slots is what is planted in the projectile array.

It covers every state the reader has to separate: a line in the water doing
nothing, a fish on the line, a bobber already being reeled in, one that has
finished and been marked inactive with its flags left behind, and a projectile
that is not a bobber at all.
*/
var slots = []struct {
	slot     int
	active   bool
	bobber   bool
	ai       [3]float32
	localAI  [3]float32
	noFloats bool
}{
	// Cast, waiting. The counter is climbing.
	{slot: 3, active: true, bobber: true, ai: [3]float32{0, 120, 0}, localAI: [3]float32{0, 412, 0}},
	// A fish on the line: the window is counting down and the catch slot holds
	// an item type.
	{slot: 7, active: true, bobber: true, ai: [3]float32{0, -3, 0}, localAI: [3]float32{0, 2290, 0}},
	// An NPC on the line, which the catch slot spells negative.
	{slot: 9, active: true, bobber: true, ai: [3]float32{0, -1, 0}, localAI: [3]float32{0, -58, 0}},
	// Already being reeled in, so not biting however the rest reads.
	{slot: 11, active: true, bobber: true, ai: [3]float32{1, -3, 0}, localAI: [3]float32{0, 2290, 0}},
	// Finished, marked inactive, flags left exactly where they were.
	{slot: 13, active: false, bobber: true, ai: [3]float32{0, -3, 0}, localAI: [3]float32{0, 2290, 0}},
	// Alive and not a bobber.
	{slot: 17, active: true, bobber: false, ai: [3]float32{0, 0, 0}, localAI: [3]float32{0, 0, 0}},
	// A bobber whose float arrays do not read, which is not a bobber anybody can
	// say anything about.
	{slot: 19, active: true, bobber: true, noFloats: true},
}

// plant builds a projectile array with those slots in it.
func plant() *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(mainBase+0x9BC, u32(arr)) // Main.projectile
	mem.PokeI32(arr+0x0C, arrayLen)

	// Every slot is allocated, as the game allocates them, and every element is
	// behind one vtable: that is what tells the array from anything else of the
	// same length.
	for i := range arrayLen {
		obj := uint32(objects + i*0x30) //nolint:gosec // a planted address
		mem.PokeBytes(arr+0x10+uint32(i)*4, u32(obj))
		mem.PokeBytes(obj, u32(0xDEADBEEF))
	}
	for i, s := range slots {
		obj := uint32(objects + s.slot*0x30) //nolint:gosec // a planted address
		if s.active {
			mem.PokeBytes(obj+0x078, []byte{1})
		}
		if s.bobber {
			mem.PokeBytes(obj+0x088, []byte{1})
		}
		if s.noFloats {
			continue
		}
		ai := uint32(floats + i*0x40)             //nolint:gosec // a planted address
		localAI := uint32(floats + i*0x40 + 0x20) //nolint:gosec // a planted address
		mem.PokeBytes(obj+0x044, u32(ai))
		mem.PokeBytes(obj+0x048, u32(localAI))
		for j := range 3 {
			mem.WriteF32(ai+0x10+uint32(j)*4, s.ai[j])
			mem.WriteF32(localAI+0x10+uint32(j)*4, s.localAI[j])
		}
	}

	// A decoy of the right length whose elements share nothing, placed in the
	// static block before the real array so a fallback scan meets it first.
	mem.PokeI32(decoy+0x0C, arrayLen)
	for i := range arrayLen {
		elem := uint32(base + 0x18000 + i*4) //nolint:gosec // a planted address
		mem.PokeBytes(decoy+0x10+uint32(i)*4, u32(elem))
		mem.PokeBytes(elem, u32(uint32(0xBBBB0000+i))) //nolint:gosec // a planted vtable
	}
	mem.PokeI32(sparse+0x0C, arrayLen)
	for i := range 100 {
		elem := uint32(base + 0x1C000 + i*4) //nolint:gosec // a planted address
		mem.PokeBytes(sparse+0x10+uint32(i)*4, u32(elem))
		mem.PokeBytes(elem, u32(0xCCCC0000))
	}

	mem.PokeBytes(mainBase+0x2000, u32(decoy))
	mem.PokeBytes(mainBase+0x2004, u32(sparse))
	mem.PokeBytes(mainBase+0x2008, u32(arr))
	return mem
}

// pyPlant is the same array as Python source.
func pyPlant(pinned bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import projectiles as P
mem = FakeMem(%d, %d)
mem.poke_bytes(%d + P.MAIN_PROJECTILE_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + P.ARRAY_LEN_OFF, %d)
for i in range(%d):
    obj = %d + i * 0x30
    mem.poke_bytes(%d + P.ARRAY_DATA_OFF + i * 4, struct.pack("<I", obj))
    mem.poke_bytes(obj, struct.pack("<I", 0xDEADBEEF))
`, base, size, mainBase, pick(pinned, arr, 0), arr, arrayLen, arrayLen, objects, arr)

	for i, s := range slots {
		obj := objects + s.slot*0x30
		if s.active {
			fmt.Fprintf(&b, "mem.poke_bytes(%d + P.ACTIVE_OFF, bytes([1]))\n", obj)
		}
		if s.bobber {
			fmt.Fprintf(&b, "mem.poke_bytes(%d + P.BOBBER_OFF, bytes([1]))\n", obj)
		}
		if s.noFloats {
			continue
		}
		ai, localAI := floats+i*0x40, floats+i*0x40+0x20
		fmt.Fprintf(&b, "mem.poke_bytes(%d + P.AI_OFF, struct.pack(\"<I\", %d))\n", obj, ai)
		fmt.Fprintf(&b, "mem.poke_bytes(%d + P.LOCALAI_OFF, struct.pack(\"<I\", %d))\n", obj, localAI)
		for j := range 3 {
			fmt.Fprintf(&b, "mem.write_f32(%d + P.ARRAY_DATA_OFF + %d * 4, %v)\n", ai, j, s.ai[j])
			fmt.Fprintf(&b, "mem.write_f32(%d + P.ARRAY_DATA_OFF + %d * 4, %v)\n", localAI, j, s.localAI[j])
		}
	}
	fmt.Fprintf(&b, `
mem.poke_i32(%d + P.ARRAY_LEN_OFF, %d)
for i in range(%d):
    elem = %d + i * 4
    mem.poke_bytes(%d + P.ARRAY_DATA_OFF + i * 4, struct.pack("<I", elem))
    mem.poke_bytes(elem, struct.pack("<I", 0xBBBB0000 + i))
mem.poke_i32(%d + P.ARRAY_LEN_OFF, %d)
for i in range(100):
    elem = %d + i * 4
    mem.poke_bytes(%d + P.ARRAY_DATA_OFF + i * 4, struct.pack("<I", elem))
    mem.poke_bytes(elem, struct.pack("<I", 0xCCCC0000))
mem.poke_bytes(%d + 0x2000, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x2004, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x2008, struct.pack("<I", %d))
`, decoy, arrayLen, arrayLen, base+0x18000, decoy,
		sparse, arrayLen, base+0x1C000, sparse,
		mainBase, decoy, mainBase, sparse, mainBase, arr)
	return b.String()
}

func pick(yes bool, a, b uint32) uint32 {
	if yes {
		return a
	}
	return b
}

// The array is found the same way, pinned or by shape, and the decoy is not it.
func TestFindingTheArrayMatchesThePython(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			var want any
			askPython(t, pyPlant(pinned)+fmt.Sprintf(
				"print(json.dumps(P.projectile_array(mem, %d)))", mainBase), &want)

			got, ok := projectile.Array(plant(), mainBase)
			require.True(t, ok, "the array was not found")
			require.Equal(t, want, asJSON(t, got), "a different array was found")
			require.Equal(t, uint32(arr), got, "and it is not the one that was planted")
		})
	}
}

// Every bobber reads the same, and the states are separated the same way.
func TestReadingBobbersMatchesThePython(t *testing.T) {
	var want []map[string]any
	askPython(t, pyPlant(true)+fmt.Sprintf(`
out = []
for b in P.find_bobbers(mem, %d):
    out.append({"slot": b.slot, "addr": b.addr, "reeling": b.reeling,
                "biting": b.biting, "catch": b.catch, "counter": b.counter})
print(json.dumps(out))`, arr), &want)

	got := projectile.FindBobbers(plant(), arr)
	require.Len(t, got, len(want), "a different number of bobbers is in the water")
	for i, w := range want {
		require.Equalf(t, w["slot"], asJSON(t, got[i].Slot), "bobber %d is in a different slot", i)
		require.Equalf(t, w["addr"], asJSON(t, got[i].Addr), "bobber %d is at a different address", i)
		require.Equalf(t, w["reeling"], got[i].Reeling(), "bobber %d reels differently", i)
		require.Equalf(t, w["biting"], got[i].Biting(), "bobber %d bites differently", i)
		require.Equalf(t, w["catch"], asJSON(t, got[i].Catch()), "bobber %d has a different catch", i)
		require.Equalf(t, w["counter"], asJSON(t, got[i].Counter()), "bobber %d counts differently", i)
	}

	/*
		And the one that matters: a finished bobber, inactive with its flags left
		behind, is not in the water. Filtering on the bobber flag alone reported
		one for minutes after it came in.
	*/
	for _, b := range got {
		require.NotEqual(t, 13, b.Slot, "an inactive bobber was reported as in the water")
	}
	require.NotEmpty(t, got, "no bobbers at all, so the comparison proves nothing")
}

// The first fish on the line is the same one.
func TestFindBiteMatchesThePython(t *testing.T) {
	var want any
	askPython(t, pyPlant(true)+fmt.Sprintf(`
b = P.find_bite(mem, %d)
print(json.dumps(None if b is None else {"slot": b.slot, "catch": b.catch}))`, arr), &want)
	require.NotNil(t, want, "the Python found no bite in a fixture that plants one")

	got, ok := projectile.FindBite(plant(), arr)
	require.True(t, ok, "no bite was found")
	require.Equal(t, want.(map[string]any)["slot"], asJSON(t, got.Slot), "a different bobber")
	require.Equal(t, want.(map[string]any)["catch"], asJSON(t, got.Catch()), "a different catch")
}

/*
An array of the right length whose elements share nothing is not the projectiles.

The game allocates every slot up front and never leaves a hole, so a real array
is fully populated with objects of one class. The length alone matches the odd
unrelated allocation.
*/
func TestADecoyArrayIsRejected(t *testing.T) {
	mem := plant()
	require.False(t, projectile.IsArray(mem, decoy), "a decoy was taken for the projectiles")
	require.False(t, projectile.IsArray(mem, sparse),
		"an array with most of its slots empty was taken for the projectiles")
	require.True(t, projectile.IsArray(mem, arr), "the real array was rejected")
}

// With nothing plausible anywhere, neither finds an array.
func TestNoArrayAtAll(t *testing.T) {
	_, ok := projectile.Array(memtest.New(base, size), mainBase)
	require.False(t, ok, "an array was found in empty memory")
}

/*
Reading one slot refuses an inactive bobber on its own.

The sweep checks that too, so the two shadow each other and neither is exercised
by the other's tests -- but a caller with a slot number in hand goes straight
here. The array holds every projectile forever and a finished one keeps its
flags, so without this check a line that came in minutes ago reads as one still
in the water.
*/
func TestReadingOneSlotRefusesAFinishedBobber(t *testing.T) {
	mem := plant()

	var want any
	askPython(t, pyPlant(true)+fmt.Sprintf(`
b = P.read_bobber(mem, %d, 13)
print(json.dumps(None if b is None else b.slot))`, arr), &want)
	require.Nil(t, want, "the Python read a finished bobber as a live one")

	_, ok := projectile.Read(mem, arr, 13)
	require.False(t, ok, "a finished bobber was read as a live one")

	// And the live one in the next slot along still reads.
	_, ok = projectile.Read(mem, arr, 7)
	require.True(t, ok, "a live bobber stopped reading")
}
