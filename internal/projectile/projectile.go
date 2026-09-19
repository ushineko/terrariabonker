/*
Package projectile finds the game's projectile array and reads the fishing
bobber's state out of it.

Read-only. Nothing here writes to the game; it exists so a cheat can ask "is a
fish biting right now?" without every caller re-deriving the layout.

The bite condition is not invented here. It is the game's own, from the method
that pulls a bobber in: for a projectile that is the local player's and has the
bobber flag, a fish is on the line when the line is still out, the bite window is
counting down, and the catch slot holds something. That slot does double duty as
the catch counter, climbing until the game rolls a catch into the same place --
and both live behind a float array reference rather than inline in the
projectile, which is why scanning the object's own bytes for a counter never
found one.

Ported from terrariabonker/projectiles.py (spec 051, step 5).
*/
package projectile

import (
	"encoding/binary"
	"math"

	"github.com/ushineko/terrariabonker/internal/layout"
)

// CounterThreshold is how far the catch counter climbs before the game rolls a
// catch into the same slot.
const CounterThreshold = 660

// Mem is the memory the projectiles are read from.
type Mem interface {
	Read(addr uint32, size int) []byte
	ReadU32(addr uint32) (uint32, bool)
	ReadI32(addr uint32) (int32, bool)
}

// Bobber is one live bobber: its slot, its address and its state.
type Bobber struct {
	Slot    int        `json:"slot"`
	Addr    uint32     `json:"addr"`
	AI      [3]float32 `json:"ai"`
	LocalAI [3]float32 `json:"local_ai"`
}

// Reeling reports whether the pull path has already claimed this bobber.
func (b Bobber) Reeling() bool { return b.AI[0] != 0 }

// Biting reports whether a catch is on the line and can still be taken.
func (b Bobber) Biting() bool {
	return !b.Reeling() && b.AI[1] < 0 && b.LocalAI[1] != 0
}

/*
Catch is what is waiting on the line: an item type, or a negative NPC type.

Only meaningful while biting. Outside the bite window that slot is the catch
counter instead, and reading it as a catch would name a fish by its progress bar.
*/
func (b Bobber) Catch() int32 {
	if !b.Biting() {
		return 0
	}
	return int32(b.LocalAI[1])
}

/*
Counter is progress toward the next catch roll.

Zero while a bite is on the line, because the game has already spent the counter
and reused the slot for the catch.
*/
func (b Bobber) Counter() float32 {
	if b.Biting() {
		return 0
	}
	return b.LocalAI[1]
}

// float3 follows a float-array field reference and reads its three elements.
func float3(mem Mem, obj uint32, fieldOff int) ([3]float32, bool) {
	ptr, ok := mem.ReadU32(obj + uint32(fieldOff)) //nolint:gosec // a field offset
	if !ok || ptr == 0 {
		return [3]float32{}, false
	}
	raw := mem.Read(ptr+layout.ArrDataOff, 12)
	if len(raw) < 12 {
		return [3]float32{}, false
	}
	var out [3]float32
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out, true
}

/*
IsArray reports whether an address looks like the projectile array: the right
length, with one shared vtable behind its elements.

The length check alone matches the odd unrelated allocation, so the elements are
sampled too. A real projectile array is fully populated with objects of one
class, because the game allocates all of them up front and never leaves a hole.
*/
func IsArray(mem Mem, ptr uint32) bool {
	if ptr < 0x10000 {
		return false
	}
	if n, ok := mem.ReadI32(ptr + layout.ArrLenOff); !ok || n != layout.ProjectileArrayLen {
		return false
	}
	raw := mem.Read(ptr+layout.ArrDataOff, layout.ProjectileArrayLen*4)
	if len(raw) < layout.ProjectileArrayLen*4 {
		return false
	}
	var elems []uint32
	for i := range layout.ProjectileArrayLen {
		if e := binary.LittleEndian.Uint32(raw[i*4:]); e > 0x10000 {
			elems = append(elems, e)
		}
	}
	if float64(len(elems)) < layout.ProjectileArrayLen*0.9 {
		return false
	}
	seen := map[uint32]bool{}
	for _, e := range elems[:min(20, len(elems))] {
		vt, _ := mem.ReadU32(e)
		seen[vt] = true
	}
	return len(seen) == 1
}

/*
Array is the address of the game's projectile array.

The known static offset is read first and what it finds is validated, then Main's
static block is scanned for the array by shape. The fallback is what found the
offset in the first place, and keeping it means a game update that moves the
field costs a slower lookup rather than a broken cheat.
*/
func Array(mem Mem, mainBase uint32) (uint32, bool) {
	if direct, ok := mem.ReadU32(mainBase + layout.MainProjectileOff); ok && IsArray(mem, direct) {
		return direct, true
	}
	blk := mem.Read(mainBase, 0x4000)
	for off := 0; off+4 <= len(blk); off += 4 {
		if ptr := binary.LittleEndian.Uint32(blk[off:]); IsArray(mem, ptr) {
			return ptr, true
		}
	}
	return 0, false
}

/*
Read is one slot read as a live bobber.

**Both flags matter, and the active one is easy to forget.** The array holds
every object forever; a finished projectile is marked inactive and its old fields
are left exactly where they were, the bobber flag included. Filtering on the
bobber flag alone therefore reports a line still in the water minutes after it
came out -- which made a reeled-in bobber look stuck for fifteen seconds, and
would have made auto-catch refuse to cast because it believed the water was busy.

The game itself checks active first. So does this.
*/
func Read(mem Mem, arr uint32, slot int) (Bobber, bool) {
	raw := mem.Read(arr+layout.ArrDataOff+uint32(slot)*4, 4) //nolint:gosec // a slot index
	if len(raw) < 4 {
		return Bobber{}, false
	}
	obj := binary.LittleEndian.Uint32(raw)
	if obj == 0 || !flagSet(mem, obj, layout.ProjectileActive) {
		return Bobber{}, false
	}
	if !flagSet(mem, obj, layout.ProjectileBobber) {
		return Bobber{}, false
	}
	ai, okAI := float3(mem, obj, layout.ProjectileAI)
	localAI, okLocal := float3(mem, obj, layout.ProjectileLocalAI)
	if !okAI || !okLocal {
		return Bobber{}, false
	}
	return Bobber{Slot: slot, Addr: obj, AI: ai, LocalAI: localAI}, true
}

// flagSet is one of a projectile's one-byte flags.
func flagSet(mem Mem, obj uint32, off int) bool {
	b := mem.Read(obj+uint32(off), 1) //nolint:gosec // a field offset
	return len(b) == 1 && b[0] == 1
}

/*
FindBobbers is every bobber currently in the water, in slot order.

Usually one. More than one is normal in multiplayer and possible alone, so a
caller that wants "the fish on my line" should take the first that is biting
rather than assume a single bobber.
*/
func FindBobbers(mem Mem, arr uint32) []Bobber {
	raw := mem.Read(arr+layout.ArrDataOff, layout.ProjectileArrayLen*4)
	if len(raw) < layout.ProjectileArrayLen*4 {
		return nil
	}
	var out []Bobber
	/*
		One read for the whole element array rather than a thousand of them: this
		runs on a poll loop tight enough to catch a bite window, and the per-slot
		version spent its time in syscalls reading pointers that are almost all
		irrelevant.
	*/
	for slot := range layout.ProjectileArrayLen {
		obj := binary.LittleEndian.Uint32(raw[slot*4:])
		if obj == 0 || !flagSet(mem, obj, layout.ProjectileActive) {
			continue
		}
		if !flagSet(mem, obj, layout.ProjectileBobber) {
			continue
		}
		if b, ok := Read(mem, arr, slot); ok {
			out = append(out, b)
		}
	}
	return out
}

// FindBite is the first bobber with a fish on the line.
func FindBite(mem Mem, arr uint32) (Bobber, bool) {
	for _, b := range FindBobbers(mem, arr) {
		if b.Biting() {
			return b, true
		}
	}
	return Bobber{}, false
}
