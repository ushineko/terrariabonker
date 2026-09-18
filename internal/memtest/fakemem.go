/*
Package memtest is a process's memory, faked, for testing what reads it.

The trainer's hard parts -- finding the player, walking mono objects, matching a
byte pattern -- are tested against a bytearray rather than a running game: the
Python suite has done that since the beginning, and the port cannot start
touching memory until Go can build the same image (spec 051, step 2).

Not a file. The Python's fake is a buffer at a base address with a few helpers
that plant structures into it, so the asset being carried across is not a blob
to load but *the shape of those structures*: where a mono string keeps its
length, how far a player's name pointer sits from their life, which way round a
field is written. Those are facts about the game, they are spelled twice while
both implementations exist, and a test plants the same things in both languages
and compares the bytes.

Nothing here reads a real process. The package is test scaffolding and is
deliberately in internal/.
*/
package memtest

import (
	"encoding/binary"
	"encoding/hex"
	"unicode/utf16"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
Mono object layout, as the Python plants it.

A 32-bit mono String is a vtable pointer, a sync block, a length in characters
and then UTF-16 -- which is why a name is read at a pointer rather than in
place, and why its length is a count of characters and not of bytes.

PlayerNameOffset is how far below a player's life field the name pointer sits.
It is negative because the life field is what the locator finds first: it is the
value a scan can recognise, and everything else is reached from it.
*/
const (
	MonoVTable       = 0xDEADBEEF
	MonoStringHeader = 12 // vtable, sync, length
	PlayerNameOffset = -0x6C0
	// PlayerBlockOffset is where a planted life/mana block starts relative to
	// the life field: the block leads with two words the game keeps in front
	// of it.
	PlayerBlockOffset = -0x08
)

/*
FakeMem is a process's memory as a buffer at a base address.

Reads and writes outside it fail rather than panic, which is what a real process
does when a region is not mapped: a locator that walks off the end of a region
has to cope with that, and a fake that panicked instead would never let it be
tested.
*/
type FakeMem struct {
	Base uint32
	Buf  []byte
	// Exec is the part of this buffer that stands for executable memory, which
	// a pattern search for JIT'd code looks at. A fake maps one buffer and the
	// caller says which slice of it is code, exactly as the Python's tests
	// replace the real region listing with a pair of addresses.
	Exec []proc.Region
}

// New is a fake of size bytes mapped at base.
func New(base uint32, size int) *FakeMem {
	return &FakeMem{Base: base, Buf: make([]byte, size)}
}

// Regions is the one region this fake maps, as a real Mem reports its own.
func (m *FakeMem) Regions() []proc.Region {
	return []proc.Region{{Start: m.Base, End: m.Base + count(len(m.Buf))}}
}

// ExecRegions is the code this fake maps, which is whatever the caller planted
// code into and said so.
func (m *FakeMem) ExecRegions() []proc.Region { return m.Exec }

/*
shift is an address at a signed offset from another.

An offset here is usually negative -- everything about a player is reached
backwards from the field a scan can recognise -- and the arithmetic is done as
int so that is expressible, then narrowed. A 32-bit game has 32-bit addresses;
that is the whole point of the type.
*/
func shift(addr uint32, delta int) uint32 {
	return uint32(int(addr) + delta) //nolint:gosec // a 32-bit address, deliberately
}

// count is a length as the game writes it. A buffer longer than four gigabytes
// is not a thing this fake can hold.
func count(n int) uint32 {
	return uint32(n) //nolint:gosec // a length, never negative and never that large
}

// at is the offset of addr in the buffer, and whether size bytes fit there.
func (m *FakeMem) at(addr uint32, size int) (int, bool) {
	if addr < m.Base {
		return 0, false
	}
	lo := int(addr - m.Base)
	if lo+size > len(m.Buf) {
		return 0, false
	}
	return lo, true
}

// Read is size bytes at addr, or nothing when they are not mapped.
func (m *FakeMem) Read(addr uint32, size int) []byte {
	lo, ok := m.at(addr, size)
	if !ok {
		return nil
	}
	out := make([]byte, size)
	copy(out, m.Buf[lo:lo+size])
	return out
}

// Write puts data at addr and reports whether it landed.
func (m *FakeMem) Write(addr uint32, data []byte) bool {
	lo, ok := m.at(addr, len(data))
	if !ok {
		return false
	}
	copy(m.Buf[lo:], data)
	return true
}

// ReadU32 is a little-endian word at addr, and whether it was there.
func (m *FakeMem) ReadU32(addr uint32) (uint32, bool) {
	b := m.Read(addr, 4)
	if b == nil {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

// ReadI32 is a signed word at addr, and whether it was there. A player's fields
// are signed, and the sign is load-bearing: a damage of -1 means an item that
// does none.
func (m *FakeMem) ReadI32(addr uint32) (int32, bool) {
	v, ok := m.ReadU32(addr)
	return int32(v), ok //nolint:gosec // a word, as its bits
}

// WriteI32 puts a signed word at addr and reports whether it landed.
func (m *FakeMem) WriteI32(addr uint32, value int32) bool {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(value)) //nolint:gosec // a word, as its bits
	return m.Write(addr, b[:])
}

// Hex is the whole buffer, which is how a test compares what two
// implementations left behind rather than what each said it did.
func (m *FakeMem) Hex() string { return hex.EncodeToString(m.Buf) }

// PokeI32 writes a signed word, which is how most of the game's fields are
// planted.
func (m *FakeMem) PokeI32(addr uint32, value int32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(value)) //nolint:gosec // a word, as its bits
	m.Write(addr, b[:])
}

// PokeBytes writes bytes at addr.
func (m *FakeMem) PokeBytes(addr uint32, data []byte) { m.Write(addr, data) }

/*
PlantMonoString writes a mono String at addr: the header, then the text as
UTF-16.

The length is in UTF-16 code units rather than bytes, which is the part worth
getting wrong once and never again: a name of five characters is ten bytes, and
reading it as ten characters walks into whatever follows it.
*/
func (m *FakeMem) PlantMonoString(addr uint32, text string) {
	units := utf16.Encode([]rune(text))

	header := make([]byte, MonoStringHeader)
	binary.LittleEndian.PutUint32(header[0:], MonoVTable)
	binary.LittleEndian.PutUint32(header[4:], 0)
	binary.LittleEndian.PutUint32(header[8:], count(len(units)))

	body := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		body = binary.LittleEndian.AppendUint16(body, unit)
	}
	m.PokeBytes(addr, append(header, body...))
}

/*
PlantPlayer writes a life/mana block and the name pointer that goes with it.

block is written from PlayerBlockOffset, in the order the game keeps it, and the
name pointer at PlayerNameOffset. Both offsets are relative to the life field
because that is what a scan finds: a number in a range worth recognising, with
everything else reached from it.
*/
func (m *FakeMem) PlantPlayer(lifeAddr uint32, block []int32, namePtr uint32) {
	for i, value := range block {
		m.PokeI32(shift(lifeAddr, PlayerBlockOffset+i*4), value)
	}
	var ptr [4]byte
	binary.LittleEndian.PutUint32(ptr[:], namePtr)
	m.Write(shift(lifeAddr, PlayerNameOffset), ptr[:])
}
