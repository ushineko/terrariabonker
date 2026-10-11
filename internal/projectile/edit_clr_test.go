package projectile_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
The projectile editor under .NET Framework (spec 052).

Planted with literals from the measurement (cmd/clrfields against 1.4.5.8 on .NET
Framework 4.8.1, docs/clr-fields-1.4.5.8.txt), not with internal/layout's
numbers: in a Projectile scale 0x078, type 0x080, timeLeft 0x09C, penetrate
0x0B4, maxPenetrate 0x0B8, extraUpdates 0x0D0, active 0x102 and tileCollide
0x112; an szarray's length at +4 and its elements from +8.

tileCollide is one byte with other bools on both sides of it (0x111 and 0x113 in
the measurement), so each neighbour is planted with something recognisable.
*/

const (
	ceBase    = 0x10000000
	ceArr     = ceBase + 0x100
	ceObjects = ceBase + 0x4000
	ceStride  = 0x140
)

// ceObj is the planted projectile behind a slot.
func ceObj(slot int) uint32 { return ceObjects + uint32(slot)*ceStride } //nolint:gosec // a slot index

// plantCLRFlying builds a CLR Projectile[] of 1001 with the same four live
// projectiles the mono tests fly and a fifth in slot 0, every other slot an
// inactive filler.
func plantCLRFlying() *memtest.FakeMem {
	mem := memtest.New(ceBase, 0x40000)
	mem.PokeI32(ceArr+0x04, 1001)
	filler := uint32(ceBase + 0x3000)
	for i := range uint32(1001) {
		mem.PokeBytes(ceArr+0x08+i*4, u32(filler))
	}
	for _, p := range flying {
		obj := ceObj(p.slot)
		mem.PokeBytes(ceArr+0x08+uint32(p.slot)*4, u32(obj)) //nolint:gosec // a slot index
		mem.PokeBytes(obj+0x102, []byte{1})                  // active
		mem.PokeI32(obj+0x080, p.ptype)                      // type
		mem.PokeBytes(obj+0x111, []byte{0xAB})
		mem.PokeBytes(obj+0x112, []byte{1}) // tileCollide
		mem.PokeBytes(obj+0x113, []byte{0xCD})
	}
	// And one in the array's first slot, which a reader that took the elements
	// to start anywhere but +8 would not see.
	mem.PokeBytes(ceArr+0x08, u32(ceObj(0)))
	mem.PokeBytes(ceObj(0)+0x102, []byte{1})
	mem.PokeI32(ceObj(0)+0x080, 14)
	return mem
}

// ceEntry is the .NET Framework entry.
func ceEntry(t *testing.T) layout.Entry {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return e
}

func ceF32(t *testing.T, mem *memtest.FakeMem, addr uint32) float32 {
	t.Helper()
	v, ok := mem.ReadU32(addr)
	require.True(t, ok)
	return math.Float32frombits(v)
}

func ceI32(t *testing.T, mem *memtest.FakeMem, addr uint32) int32 {
	t.Helper()
	v, ok := mem.ReadI32(addr)
	require.True(t, ok)
	return v
}

/*
A sweep under the CLR writes each override where the CLR keeps the field, at the
field's own width, on the projectiles of the types named and no others.
*/
func TestACLRSweepWritesTheOverrides(t *testing.T) {
	mem := plantCLRFlying()
	got := projectile.NewEditor().Sweep(mem, ceEntry(t), ceArr, overrides)

	require.Equal(t, map[int32]int{985: 2, 14: 2}, got.Types)
	require.Equal(t, 12, got.Patched, "five fields on each of two, one on each of the other two")
	require.InDelta(t, 0.5, ceF32(t, mem, ceObj(0)+0x078), 0, "the first slot was not swept")

	for _, slot := range []int{2, 5} {
		obj := ceObj(slot)
		require.EqualValuesf(t, 2, ceI32(t, mem, obj+0x0D0), "slot %d extraUpdates", slot)
		require.EqualValuesf(t, -1, ceI32(t, mem, obj+0x0B4), "slot %d penetrate", slot)
		require.EqualValuesf(t, -1, ceI32(t, mem, obj+0x0B8), "slot %d maxPenetrate", slot)
		require.InDeltaf(t, 2.0, ceF32(t, mem, obj+0x078), 0, "slot %d scale", slot)
		require.EqualValuesf(t, 30000, ceI32(t, mem, obj+0x09C), "slot %d timeLeft", slot)
		require.Equalf(t, []byte{0xAB, 0x00, 0xCD}, mem.Read(obj+0x111, 3),
			"slot %d: tileCollide was not cleared, or the write reached a neighbour", slot)
		require.EqualValuesf(t, 985, ceI32(t, mem, obj+0x080), "slot %d: the type was written", slot)
	}
	require.InDelta(t, 0.5, ceF32(t, mem, ceObj(8)+0x078), 0)
	require.EqualValues(t, 0, ceI32(t, mem, ceObj(8)+0x0D0), "a field of another type's override was written")

	// The type nothing overrides is as it was planted.
	other := ceObj(12)
	require.Zero(t, ceI32(t, mem, other+0x078))
	require.Equal(t, []byte{0xAB, 0x01, 0xCD}, mem.Read(other+0x111, 3))
}

// An inactive projectile under the CLR is left alone, whatever type it holds:
// the array keeps every object forever and a finished one still reads its type.
func TestACLRSweepSkipsWhatIsNotInFlight(t *testing.T) {
	mem := plantCLRFlying()
	mem.PokeBytes(ceObj(5)+0x102, []byte{0})
	before := mem.Read(ceObj(5), ceStride)

	got := projectile.NewEditor().Sweep(mem, ceEntry(t), ceArr, overrides)
	require.Equal(t, map[int32]int{985: 1, 14: 2}, got.Types)
	require.Equal(t, before, mem.Read(ceObj(5), ceStride), "a finished projectile was written")
}

// The lifetime is applied once per projectile under the CLR too, and again to a
// different object that turns up in the same slot.
func TestTheCLRLifetimeIsAppliedOnce(t *testing.T) {
	mem := plantCLRFlying()
	ed := projectile.NewEditor()
	entry := ceEntry(t)
	life := map[int32]map[string]float64{985: {"timeLeft": 30000}}

	require.Equal(t, 2, ed.Sweep(mem, entry, ceArr, life).Patched)
	mem.PokeI32(ceObj(2)+0x09C, 29990) // the game spends it
	require.Zero(t, ed.Sweep(mem, entry, ceArr, life).Patched, "the lifetime was pinned")
	require.EqualValues(t, 29990, ceI32(t, mem, ceObj(2)+0x09C))

	// A new object in slot 2, of the same type.
	fresh := ceObj(40)
	mem.PokeBytes(ceArr+0x08+2*4, u32(fresh))
	mem.PokeBytes(fresh+0x102, []byte{1})
	mem.PokeI32(fresh+0x080, 985)
	require.Equal(t, 1, ed.Sweep(mem, entry, ceArr, life).Patched)
	require.EqualValues(t, 30000, ceI32(t, mem, fresh+0x09C))
}

// The mono numbers find nothing to edit in a CLR array, and the CLR numbers
// nothing in a mono one: a sweep runs with its own entry's layout or writes
// nothing.
func TestASweepUsesItsOwnEntrysNumbers(t *testing.T) {
	clr := plantCLRFlying()
	before := clr.Hex()
	got := projectile.NewEditor().Sweep(clr, layout.Mono(), ceArr, overrides)
	require.Zero(t, got.Patched)
	require.Equal(t, before, clr.Hex(), "mono's offsets wrote into a CLR array")

	mono := plantFlying()
	before = mono.Hex()
	got = projectile.NewEditor().Sweep(mono, ceEntry(t), arr, overrides)
	require.Zero(t, got.Patched)
	require.Equal(t, before, mono.Hex(), "the CLR's offsets wrote into a mono array")
}
