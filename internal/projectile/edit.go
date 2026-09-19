package projectile

import (
	"encoding/binary"
	"math"
	"sort"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Per-type overrides, enforced on whatever is currently in flight.

Nothing here persists in the game. The method that gives a projectile its
defaults assigns literals from its own chain and never consults the sample
collection, so there is no write that makes a change stick -- every field has to
be re-applied to each live projectile. That is the whole reason this is a sweep
rather than a one-shot edit.
*/

// Kind is how wide a field is and how it is written.
type Kind string

// The three widths. The distinction is not a detail: a one-byte flag has another
// field packed behind it, and a four-byte write of one reaches into it.
const (
	I32 Kind = "i32"
	F32 Kind = "f32"
	B8  Kind = "b8"
)

/*
Field is one editable field: where it lives, how wide it is, and what it may be
set to.

Once marks a field applied when a projectile is first seen rather than enforced
on every sweep -- see Editor for why the lifetime must be one of those.
*/
type Field struct {
	Offset int
	Kind   Kind
	Lo, Hi float64
	Label  string
	Once   bool
}

// Clamp bounds a value to the field's range, in the field's own type.
func (f Field) Clamp(value float64) float64 {
	v := math.Max(f.Lo, math.Min(f.Hi, value))
	if f.Kind == F32 {
		return v
	}
	return math.RoundToEven(v)
}

/*
Fields is the editable set, deliberately small.

The metadata walker makes it trivial to offer all hundred-odd fields of a
projectile, and most of them are a crash or a self-inflicted death. The AI style
runs a behaviour against slots that mean something else under it; the
friend-or-foe flags turn the player's own projectiles on the player. Neither is
reachable from here.
*/
var Fields = map[string]Field{
	"tileCollide":  {Offset: layout.ProjectileTileCollide, Kind: B8, Lo: 0, Hi: 1, Label: "Pass through blocks (0 = yes)"},
	"penetrate":    {Offset: layout.ProjectilePenetrate, Kind: I32, Lo: -1, Hi: 999, Label: "Enemies pierced (-1 = infinite)"},
	"extraUpdates": {Offset: layout.ProjectileExtraUpdates, Kind: I32, Lo: 0, Hi: 16, Label: "Extra ticks per frame (speed)"},
	"scale":        {Offset: layout.ProjectileScale, Kind: F32, Lo: 0.05, Hi: 10.0, Label: "Size"},
	"timeLeft":     {Offset: layout.ProjectileTimeLeft, Kind: I32, Lo: 1, Hi: 216000, Label: "Lifetime in ticks", Once: true},
}

// WriteMem is the memory an override is written into.
type WriteMem interface {
	Mem
	Write(addr uint32, data []byte) bool
	WriteI32(addr uint32, value int32) bool
}

/*
write puts one field at its own width.

Width is not a detail. The collide flag is a single byte and the next field
begins at the following word, so a four-byte write of a flag reaches into
whatever is packed behind it. The probe that preceded this did exactly that.
*/
func write(mem WriteMem, addr uint32, field Field, value float64) {
	at := addr + uint32(field.Offset) //nolint:gosec // a field offset
	switch field.Kind {
	case F32:
		mem.Write(at, binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(value))))
	case B8:
		var b byte
		if value != 0 {
			b = 1
		}
		mem.Write(at, []byte{b})
	default:
		mem.WriteI32(at, int32(value)) //nolint:gosec // clamped to the field's range
	}
}

/*
Editor applies per-type overrides to whatever is currently in flight.

Stateful across sweeps for one reason: some fields have to be applied **once per
projectile** rather than enforced.

The lifetime is the case that forces it. Pinning it every sweep means no
projectile ever expires, and the game allocates every slot up front -- so a
pinned lifetime fills the array and the player's weapons quietly stop firing.
Applied once, a raised lifetime is a bigger budget the game then spends normally,
which is what somebody asking for "skulls that cross a wall" actually wants: the
behaviour drains the budget faster inside solid tile, so the budget is what
decides how far it gets.

A projectile is new when its slot holds a different object or a different type
than the last sweep saw. Slots are recycled constantly by fast weapons, so
identity has to come from the object rather than the index.
*/
type Editor struct {
	seen map[int]seenProjectile
}

// seenProjectile is what was in a slot last sweep.
type seenProjectile struct {
	addr  uint32
	ptype int32
}

// NewEditor is an editor that has seen nothing.
func NewEditor() *Editor { return &Editor{seen: map[int]seenProjectile{}} }

// Forget drops per-projectile state, so every projectile counts as new again.
func (e *Editor) Forget() { e.seen = map[int]seenProjectile{} }

// Swept is what one sweep did.
type Swept struct {
	Patched int           `json:"patched"`
	Types   map[int32]int `json:"types"`
}

/*
Sweep applies the overrides once over the array, and is what it did.

Counts rather than a bare total: "we wrote forty fields" says nothing about
whether the right projectiles were touched, and the caller reports to a player.
*/
func (e *Editor) Sweep(mem WriteMem, arr uint32, overrides map[int32]map[string]float64) Swept {
	out := Swept{Types: map[int32]int{}}
	if len(overrides) == 0 {
		e.Forget()
		return out
	}
	raw := mem.Read(arr+layout.ArrDataOff, layout.ProjectileArrayLen*4)
	if len(raw) < layout.ProjectileArrayLen*4 {
		return out
	}

	live := map[int]seenProjectile{}
	for slot := range layout.ProjectileArrayLen {
		obj := binary.LittleEndian.Uint32(raw[slot*4:])
		if obj == 0 || !flagSet(mem, obj, layout.ProjectileActive) {
			continue
		}
		ptype, _ := mem.ReadI32(obj + uint32(layout.ProjectileType)) //nolint:gosec // a field offset
		wanted, ok := overrides[ptype]
		if !ok {
			continue
		}
		here := seenProjectile{addr: obj, ptype: ptype}
		live[slot] = here
		fresh := e.seen[slot] != here

		// In name order, so a sweep writes the same fields in the same sequence
		// however the caller built the map.
		for _, name := range sortedNames(wanted) {
			field, known := Fields[name]
			if !known || (field.Once && !fresh) {
				continue
			}
			value := field.Clamp(wanted[name])
			write(mem, obj, field, value)
			/*
				The defaults method ends by setting the maximum pierce from the
				pierce, and the game treats them as a pair -- so writing one alone
				leaves it accounting inconsistently.
			*/
			if name == "penetrate" {
				mem.WriteI32(obj+uint32(layout.ProjectileMaxPenetrate), int32(value)) //nolint:gosec // clamped above
			}
			out.Patched++
		}
		out.Types[ptype]++
	}
	/*
		Only slots seen this sweep are remembered: a slot the game has reused must
		not match a projectile that left it, or the once-only fields would never
		re-apply.
	*/
	e.seen = live
	return out
}

// sortedNames is an override's field names in order.
func sortedNames(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
