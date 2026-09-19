package projectile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
The projectile editor, and the one rule that is not obvious.

Nothing it writes persists: the game assigns a projectile's defaults from
literals and never consults its own sample collection, so every field has to be
re-applied to each live projectile. That makes this a sweep -- and it makes the
lifetime dangerous, because pinning that every sweep means nothing ever expires,
the array fills, and the player's weapons quietly stop firing. So the lifetime is
applied once per projectile, and "once" is decided by the object rather than the
slot, since fast weapons recycle slots constantly.
*/

// A few live projectiles of two types, in slots the sweep has to find.
var flying = []struct {
	slot  int
	ptype int32
}{
	{slot: 2, ptype: 985},
	{slot: 5, ptype: 985},
	{slot: 8, ptype: 14},
	{slot: 12, ptype: 999}, // a type nothing overrides
}

// plantFlying builds an array of live projectiles.
func plantFlying() *memtest0 {
	mem := plant()
	for _, p := range flying {
		obj := uint32(objects + p.slot*0x30) //nolint:gosec // a planted address
		mem.PokeBytes(obj+0x078, []byte{1})  // active
		mem.PokeI32(obj+0x094, p.ptype)      // type
		/*
			Something recognisable in the three bytes behind the collide flag.

			That flag is one byte and the next field begins at the following
			word, so a four-byte write of it reaches into these. Left zeroed,
			a wide write and a narrow one leave the same bytes and the
			difference is invisible -- which is how the probe that preceded this
			got away with it.
		*/
		mem.PokeBytes(obj+0x101, []byte{0xAB, 0xCD, 0xEF})
	}
	return mem
}

// overrides is what the caller asks for, covering all three field widths and the
// once-only one.
const pyOverrides = `{985: {"extraUpdates": 2, "penetrate": -1, "scale": 2.0,
                            "tileCollide": 0, "timeLeft": 30000},
                      14: {"scale": 0.5}}`

var overrides = map[int32]map[string]float64{
	985: {"extraUpdates": 2, "penetrate": -1, "scale": 2.0, "tileCollide": 0, "timeLeft": 30000},
	14:  {"scale": 0.5},
}

/*
Writing the pierce also writes the maximum pierce.

The defaults method ends by setting one from the other and the game treats them
as a pair, so writing one alone leaves it accounting inconsistently.
*/
func TestThePierceIsWrittenAsAPair(t *testing.T) {
	mem := plantFlying()
	projectile.NewEditor().Sweep(mem, arr, map[int32]map[string]float64{
		985: {"penetrate": 7},
	})

	obj := uint32(objects + 2*0x30)
	pierce, _ := mem.ReadI32(obj + 0x0D4)
	maxPierce, _ := mem.ReadI32(obj + 0x0DC)
	require.Equal(t, int32(7), pierce, "the pierce was not written")
	require.Equal(t, int32(7), maxPierce, "the maximum pierce was left behind")
}

// With nothing to apply, a sweep writes nothing and forgets what it had seen.
func TestAnEmptySweep(t *testing.T) {
	mem := plantFlying()
	before := mem.Hex()
	ed := projectile.NewEditor()

	got := ed.Sweep(mem, arr, nil)
	require.Zero(t, got.Patched)
	require.Empty(t, got.Types)
	require.Equal(t, before, mem.Hex(), "an empty sweep wrote something")

	// And what it had seen is gone, so the next real sweep applies the once-only
	// fields again.
	ed.Sweep(mem, arr, overrides)
	full := ed.Sweep(mem, arr, overrides)
	ed.Sweep(mem, arr, nil)
	again := ed.Sweep(mem, arr, overrides)
	require.Greater(t, again.Patched, full.Patched,
		"an empty sweep did not drop what it had seen")
}

/*
A one-byte flag is written one byte wide.

The collide flag has three bytes of padding behind it and the next field after
that. A four-byte write of the flag reaches into them, which is what the probe
that preceded this module did -- and it is invisible unless something
recognisable is there to be clobbered.
*/
func TestAFlagIsWrittenAtItsOwnWidth(t *testing.T) {
	mem := plantFlying()
	projectile.NewEditor().Sweep(mem, arr, map[int32]map[string]float64{
		985: {"tileCollide": 0},
	})

	obj := uint32(objects + 2*0x30)
	require.Equal(t, []byte{0}, mem.Read(obj+0x100, 1), "the flag was not written")
	require.Equal(t, []byte{0xAB, 0xCD, 0xEF}, mem.Read(obj+0x101, 3),
		"writing the flag reached into what is packed behind it")
}
