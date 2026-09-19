package projectile_test

import (
	"fmt"
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

// pyFlying is the same, as Python source.
func pyFlying() string {
	out := pyPlant(true)
	for _, p := range flying {
		obj := objects + p.slot*0x30
		out += fmt.Sprintf("mem.poke_bytes(%d + P.ACTIVE_OFF, bytes([1]))\n", obj)
		out += fmt.Sprintf("mem.poke_i32(%d + P.TYPE_OFF, %d)\n", obj, p.ptype)
		out += fmt.Sprintf("mem.poke_bytes(%d + P.TILECOLLIDE_OFF + 1, bytes([0xAB, 0xCD, 0xEF]))\n", obj)
	}
	return out + `
from terrariabonker.projectile_edit import ProjectileEditor
ed = ProjectileEditor()
`
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
A sweep writes the same bytes and reports the same counts.

Then a second sweep over the same projectiles, which must write less: the
lifetime is applied once and the rest are enforced.
*/
func TestSweepMatchesThePython(t *testing.T) {
	var want struct {
		First  map[string]any `json:"first"`
		Second map[string]any `json:"second"`
		Buf    string         `json:"buf"`
	}
	askPython(t, pyFlying()+fmt.Sprintf(`
first = ed.sweep(mem, %d, %s)
second = ed.sweep(mem, %d, %s)
print(json.dumps({"first": first, "second": second, "buf": mem.buf.hex()}))`,
		arr, pyOverrides, arr, pyOverrides), &want)

	mem := plantFlying()
	ed := projectile.NewEditor()

	first := ed.Sweep(mem, arr, overrides)
	require.Equal(t, want.First["patched"], asJSON(t, first.Patched),
		"a different number of fields was written")
	require.Equal(t, want.First["types"], asJSON(t, first.Types),
		"a different set of types was touched")

	second := ed.Sweep(mem, arr, overrides)
	require.Equal(t, want.Second["patched"], asJSON(t, second.Patched),
		"the second sweep wrote a different number of fields")
	require.Less(t, second.Patched, first.Patched,
		"the second sweep wrote as much as the first, so nothing is applied once")
	require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
}

/*
A slot the game has reused is a new projectile, so its once-only fields apply
again.

Fast weapons recycle slots constantly. Identity taken from the slot index would
mean a new projectile inheriting the last one's "already done", and its lifetime
would never be raised.
*/
func TestAReusedSlotIsANewProjectile(t *testing.T) {
	var want map[string]any
	askPython(t, pyFlying()+fmt.Sprintf(`
ed.sweep(mem, %d, %s)
same = ed.sweep(mem, %d, %s)
# The game reuses slot 2 for another projectile of the same type.
mem.poke_bytes(%d + P.ARRAY_DATA_OFF + 2 * 4, struct.pack("<I", %d))
mem.poke_bytes(%d + P.ACTIVE_OFF, bytes([1]))
mem.poke_i32(%d + P.TYPE_OFF, 985)
reused = ed.sweep(mem, %d, %s)
print(json.dumps({"same": same["patched"], "reused": reused["patched"]}))`,
		arr, pyOverrides, arr, pyOverrides,
		arr, objects+0x30*40, objects+0x30*40, objects+0x30*40,
		arr, pyOverrides), &want)

	mem := plantFlying()
	ed := projectile.NewEditor()
	ed.Sweep(mem, arr, overrides)
	same := ed.Sweep(mem, arr, overrides)

	// The game reuses that slot for another projectile of the same type.
	fresh := uint32(objects + 0x30*40)
	mem.PokeBytes(arr+0x10+2*4, u32(fresh))
	mem.PokeBytes(fresh+0x078, []byte{1})
	mem.PokeI32(fresh+0x094, 985)
	reused := ed.Sweep(mem, arr, overrides)

	require.Equal(t, want["same"], asJSON(t, same.Patched), "a settled sweep differs")
	require.Equal(t, want["reused"], asJSON(t, reused.Patched), "a reused slot differs")
	require.Greater(t, reused.Patched, same.Patched,
		"a reused slot did not count as a new projectile")
}

// Forgetting makes every projectile new again.
func TestForgettingMatchesThePython(t *testing.T) {
	var want map[string]any
	askPython(t, pyFlying()+fmt.Sprintf(`
first = ed.sweep(mem, %d, %s)
ed.forget()
after = ed.sweep(mem, %d, %s)
print(json.dumps({"first": first["patched"], "after": after["patched"]}))`,
		arr, pyOverrides, arr, pyOverrides), &want)

	mem := plantFlying()
	ed := projectile.NewEditor()
	first := ed.Sweep(mem, arr, overrides)
	ed.Forget()
	after := ed.Sweep(mem, arr, overrides)

	require.Equal(t, want["after"], asJSON(t, after.Patched), "forgetting differs")
	require.Equal(t, first.Patched, after.Patched,
		"after forgetting, a projectile was not treated as new")
}

// Every field is clamped to its own range, in its own type.
func TestClampingMatchesThePython(t *testing.T) {
	values := []float64{-1000, -1.5, -1, 0, 0.04, 0.5, 1, 2.5, 16, 17, 999, 1000, 300000}

	for _, name := range []string{"tileCollide", "penetrate", "extraUpdates", "scale", "timeLeft"} {
		t.Run(name, func(t *testing.T) {
			var want []float64
			askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker.projectile_edit import FIELDS
f = FIELDS[%q]
print(json.dumps([f.clamp(v) for v in %s]))`, name, pyFloats(values)), &want)

			field := projectile.Fields[name]
			for i, v := range values {
				require.InDeltaf(t, want[i], field.Clamp(v), 1e-9,
					"%s clamps %v differently", name, v)
			}
		})
	}
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
