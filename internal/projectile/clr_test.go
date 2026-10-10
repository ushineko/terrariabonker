package projectile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
Under .NET Framework the projectile array is Main.projectile's reference static:
a Projectile[] whose length is at +0x04 and first element at +0x08, each element
a Projectile pointer. A Projectile keeps ai at +0x40 and localAI at +0x44 (float[]
references, whose three floats are at the array's own +0x08), and the active and
bobber flags at +0x102 and +0x104.

A 1001-slot array is planted at those offsets, reached through the reference-static
slot, with a few bobbers in known states and the rest a shared filler. Locate
finds the array by shape and the readers read each bobber back -- the same states
the mono read test covers, at the CLR layout.
*/
func TestCLRProjectileReads(t *testing.T) {
	const clrBase = 0x10000000
	mem := memtest.New(clrBase, 0x40000)

	const (
		slot   = clrBase + 0x40  // Main.projectile reference-static slot
		arr    = clrBase + 0x100 // the Projectile[] array object
		vtable = 0x0823c000      // one shared element class
		count  = 1001
	)
	mem.PokeI32(slot, arr)
	mem.PokeI32(arr+0x00, vtable) // MethodTable (unused by the reader)
	mem.PokeI32(arr+0x04, count)  // length (ArrLenOff 0x04)
	data := uint32(arr + 0x08)    // ArrDataOff 0x08

	// Every slot is populated, as the game populates them, all behind one vtable:
	// that is what tells the array from anything else of the same length. The
	// filler is one inactive object, so only the planted slots read as bobbers.
	filler := uint32(clrBase + 0x3000)
	mem.PokeI32(filler, vtable)
	for i := 0; i < count; i++ {
		mem.PokeI32(data+uint32(i)*4, int32(filler)) //nolint:gosec // a planted pointer
	}

	plant := func(k, s int, ai, localAI [3]float32) {
		obj := uint32(clrBase + 0x4000 + k*0x120) //nolint:gosec // a planted address
		fa := uint32(clrBase + 0x20000 + k*0x40)  //nolint:gosec // a planted address
		fb := fa + 0x20
		mem.PokeI32(data+uint32(s)*4, int32(obj)) //nolint:gosec // a planted pointer
		mem.PokeI32(obj, vtable)
		mem.PokeBytes(obj+0x102, []byte{1}) // active
		mem.PokeBytes(obj+0x104, []byte{1}) // bobber
		mem.PokeI32(obj+0x40, int32(fa))    //nolint:gosec // ai float[] ref
		mem.PokeI32(obj+0x44, int32(fb))    //nolint:gosec // localAI float[] ref
		for j := 0; j < 3; j++ {
			mem.WriteF32(fa+0x08+uint32(j)*4, ai[j])      //nolint:gosec // ArrDataOff 0x08
			mem.WriteF32(fb+0x08+uint32(j)*4, localAI[j]) //nolint:gosec // ArrDataOff 0x08
		}
	}
	// Cast and waiting: the counter is climbing, nothing has bitten.
	plant(0, 3, [3]float32{0, 120, 0}, [3]float32{0, 412, 0})
	// A fish on the line: the window counts down and the catch slot holds a type.
	plant(1, 7, [3]float32{0, -3, 0}, [3]float32{0, 2290, 0})
	// Already being reeled in, so not biting however the rest reads.
	plant(2, 11, [3]float32{1, -3, 0}, [3]float32{0, 2290, 0})

	e := clrEntry()
	v, ok := projectile.Locate(mem, e, slot)
	require.True(t, ok, "the projectile array was not found under the CLR")
	require.EqualValues(t, arr, v.Arr(), "a different array was found")

	got := v.FindBobbers()
	require.Len(t, got, 3, "a different number of live bobbers was found")
	bySlot := map[int]projectile.Bobber{}
	for _, b := range got {
		bySlot[b.Slot] = b
	}

	waiting := bySlot[3]
	require.False(t, waiting.Reeling(), "a waiting bobber reads as being reeled in")
	require.False(t, waiting.Biting(), "a waiting bobber reads as having a bite")
	require.EqualValues(t, 412, waiting.Counter())

	reeling := bySlot[11]
	require.True(t, reeling.Reeling())
	require.False(t, reeling.Biting(), "a bobber already being reeled read as a fresh bite")

	bite, biting := v.FindBite()
	require.True(t, biting, "the fish on the line was not found")
	require.Equal(t, 7, bite.Slot, "a bobber that is not biting was chosen")
	require.EqualValues(t, 2290, bite.Catch())
}

func clrEntry() layout.Entry {
	e, _ := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	return e
}
