/*
Package buffs is the player's active effects.

A buff is nothing more than a type and a time in two parallel arrays that the
game counts down once per frame. That is the whole mechanism, and it is why
holding an effect up needs no code patch: writing the pair is what the game
itself does, and a buff whose time stops being renewed expires on its own.

Ported from terrariabonker/buffs.py (spec 051, step 5).
*/
package buffs

import (
	"encoding/binary"
	"fmt"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
DefaultTicks is what "added" means when nothing says otherwise.

Long enough that a renewal loop running a few times a second cannot let it lapse
between rounds, short enough that the buff is visibly gone a moment after the
trainer stops renewing it.
*/
const DefaultTicks = 120 // two seconds at sixty frames

// Mem is the memory the buffs are read from and written to.
type Mem interface {
	Read(addr uint32, size int) []byte
	Write(addr uint32, data []byte) bool
}

// Buffs reads and renews one player's effects.
type Buffs struct {
	Mem  Mem
	Life uint32
}

// New is the buffs of the player whose statLife is at life.
func New(mem Mem, life uint32) *Buffs { return &Buffs{Mem: mem, Life: life} }

/*
array is where one of the two arrays starts and how long it is.

The length is checked rather than trusted: a null or rotted pointer reads as
something, and a buff array of a hundred thousand slots is a pointer that is not
one.
*/
func (b *Buffs) array(ptrOff int) (uint32, int, error) {
	raw := b.Mem.Read(uint32(int64(b.Life)+int64(ptrOff)), 4) //nolint:gosec // a delta from statLife
	if len(raw) < 4 {
		return 0, 0, fmt.Errorf("could not read the buff arrays")
	}
	ptr := binary.LittleEndian.Uint32(raw)
	if ptr == 0 {
		return 0, 0, fmt.Errorf("buff array pointer is null -- is a player loaded?")
	}
	head := b.Mem.Read(ptr+layout.ArrLenOff, 4)
	if len(head) < 4 {
		return 0, 0, fmt.Errorf("could not read the buff arrays")
	}
	n := int(binary.LittleEndian.Uint32(head))
	if n <= 0 || n > 256 {
		return 0, 0, fmt.Errorf("buff array length %d is not believable", n)
	}
	return ptr + layout.ArrDataOff, n, nil
}

// Slots is how many buffs the player can hold at once.
func (b *Buffs) Slots() (int, error) {
	_, n, err := b.array(layout.BuffTypePtrOff)
	return n, err
}

// read is one of the arrays, whole.
func (b *Buffs) read(ptrOff int) ([]int32, error) {
	base, n, err := b.array(ptrOff)
	if err != nil {
		return nil, err
	}
	raw := b.Mem.Read(base, n*4)
	if len(raw) < n*4 {
		return nil, fmt.Errorf("could not read the buff arrays")
	}
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(binary.LittleEndian.Uint32(raw[i*4:])) //nolint:gosec // a field, as its bits
	}
	return out, nil
}

// Active is one occupied slot: which buff, and how long it has left.
type Active struct {
	Slot  int   `json:"slot"`
	Type  int32 `json:"type"`
	Ticks int32 `json:"ticks"`
}

// Active is every occupied slot.
func (b *Buffs) Active() ([]Active, error) {
	types, err := b.read(layout.BuffTypePtrOff)
	if err != nil {
		return nil, err
	}
	times, err := b.read(layout.BuffTimePtrOff)
	if err != nil {
		return nil, err
	}
	var out []Active
	for i, t := range types {
		if t != 0 && i < len(times) {
			out = append(out, Active{Slot: i, Type: t, Ticks: times[i]})
		}
	}
	return out, nil
}

// TimeOf is how long a buff has left, or nothing when it is not running.
func (b *Buffs) TimeOf(buffType int32) int32 {
	active, err := b.Active()
	if err != nil {
		return 0
	}
	for _, a := range active {
		if a.Type == buffType {
			return a.Ticks
		}
	}
	return 0
}

// What a renewal did.
const (
	Kept    = "kept"
	Renewed = "renewed"
	Added   = "added"
	Full    = "full"
)

/*
Renew gives a buff at least this much time left, and never takes time away.

**The refusal to shorten is the point, not an optimisation.** Somebody who drank
a potion has eight minutes of it; renewing that to two seconds would leave them
with two seconds the moment they dropped the stack, and they would blame the
potion rather than the trainer. So a slot already running longer is left
completely alone -- not rewritten with the larger of the two values, not touched
at all.
*/
func (b *Buffs) Renew(buffType, ticks int32) (string, error) {
	if buffType <= 0 {
		return "", fmt.Errorf("buff type must be positive")
	}
	if ticks <= 0 {
		return "", fmt.Errorf("ticks must be positive")
	}
	types, err := b.read(layout.BuffTypePtrOff)
	if err != nil {
		return "", err
	}
	times, err := b.read(layout.BuffTimePtrOff)
	if err != nil {
		return "", err
	}
	typeBase, _, err := b.array(layout.BuffTypePtrOff)
	if err != nil {
		return "", err
	}
	timeBase, _, err := b.array(layout.BuffTimePtrOff)
	if err != nil {
		return "", err
	}

	for i, t := range types {
		if t != buffType {
			continue
		}
		if i < len(times) && times[i] >= ticks {
			return Kept, nil
		}
		b.Mem.Write(timeBase+uint32(i)*4, word(ticks)) //nolint:gosec // a slot index
		return Renewed, nil
	}
	for i, t := range types {
		if t != 0 {
			continue
		}
		/*
			Time first, type last. Until the type is set the game ignores the
			slot, so a half-written one is an empty slot rather than a buff with
			no time on it -- the same ordering the NPC spawn uses for its active
			flag.
		*/
		b.Mem.Write(timeBase+uint32(i)*4, word(ticks))    //nolint:gosec // a slot index
		b.Mem.Write(typeBase+uint32(i)*4, word(buffType)) //nolint:gosec // a slot index
		return Added, nil
	}
	return Full, nil
}

// word is a value as the game stores it.
func word(v int32) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(v)) //nolint:gosec // a word, as its bits
}
