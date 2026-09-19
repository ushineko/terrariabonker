package projectile_test

import (
	"path/filepath"
	"runtime"
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

func pick(yes bool, a, b uint32) uint32 {
	if yes {
		return a
	}
	return b
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
