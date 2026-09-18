/*
Package locate finds the player in a running game's memory.

No addresses are hardcoded. Terraria's objects live on the managed heap, which
moves, so the player is found by looking for what a player *looks like*: six
consecutive integers that could be a life and mana block, with a mono string a
fixed distance away that reads as a name. That pairing is what makes a scan of
about 1.6 GB return the player's own copies and little else.

This is the Go half of terrariabonker/locate.py (spec 051, step 3). Every rule in
it -- what counts as a plausible block, what counts as a name -- is compared with
the Python's answer over the same planted memory while both exist, because being
wrong here is not an error message: it is a write to an address that belongs to
something else.

Nothing in this package opens a process. It reads whatever Mem it is given,
which is a real one for the CLI and a planted buffer for a test.
*/
package locate

import (
	"encoding/binary"
	"unicode/utf16"

	"github.com/ushineko/terrariabonker/internal/proc"
)

// Mem is the memory a locator reads: which parts of it exist, and what is in
// them.
type Mem interface {
	// Regions is the writable, scannable memory.
	Regions() []proc.Region
	// Read is size bytes at addr, or nothing when they are not readable. A
	// short answer is ordinary: a region can end mid-read.
	Read(addr uint32, size int) []byte
}

/*
Where a player's parts sit relative to the field a scan can recognise.

NameOffset is negative because statLife is what is found first: it is the value
worth looking for, and the name is reached backwards from it. BlockLen is the
six integers the game keeps together, and the block starts two words before
statLife.
*/
const (
	NameOffset = -0x6C0
	BlockLen   = 6
	blockStart = -8
)

/*
BoostHeadroom is how far equipment and buffs may lift a cap above its permanent
value.

Generous on purpose: the number only has to keep random memory out. This used to
require the boosted cap to equal the permanent one, and it cost a real player --
with a mana accessory on (max 200, boosted 220, currently 220) the live player
failed validation, the scan fell back to an inert load-time snapshot, and every
write the trainer made landed on a copy the game ignores. The cheats silently
did nothing.
*/
const BoostHeadroom = 500

// Block is one matched player copy: where its statLife is, the six fields, and
// the name that confirmed it.
type Block struct {
	LifeAddr     uint32
	StatLifeMax2 int32
	StatLifeMax  int32
	StatLife     int32
	StatMana     int32
	StatManaMax  int32
	StatManaMax2 int32
	Name         string
}

// Fields is the block as the game keeps it, in order.
func (b Block) Fields() []int32 {
	return []int32{b.StatLifeMax2, b.StatLifeMax, b.StatLife,
		b.StatMana, b.StatManaMax, b.StatManaMax2}
}

/*
ValidBlock reports whether six integers look like a life and mana block.

The order is statLifeMax2, statLifeMax, statLife, statMana, statManaMax,
statManaMax2. The Max2 fields are the caps *after* equipment and buffs, so they
are at least their permanent counterparts rather than equal to them, and the
current value is bounded by the boosted cap rather than the permanent one.
*/
func ValidBlock(v []int32) bool {
	if len(v) < BlockLen {
		return false
	}
	lifeMax2, lifeMax, life, mana, manaMax, manaMax2 := v[0], v[1], v[2], v[3], v[4], v[5]
	// Life: crystals take the permanent cap to 400 in twenty steps, fruit to
	// 500 in five.
	return 100 <= lifeMax && lifeMax <= 500 && lifeMax%5 == 0 &&
		lifeMax <= lifeMax2 && lifeMax2 <= lifeMax+BoostHeadroom &&
		1 <= life && life <= lifeMax2 &&
		// Mana: crystals only, so the permanent cap is a multiple of 20 up to 400.
		20 <= manaMax && manaMax <= 400 && manaMax%20 == 0 &&
		manaMax <= manaMax2 && manaMax2 <= manaMax+BoostHeadroom &&
		0 <= mana && mana <= manaMax2
}

/*
ReadMonoString decodes a 32-bit mono String, or reports that it is not one.

Only a name-plausible length of printable ASCII is accepted, and that is what
makes this a validator rather than a coincidence sink: any four bytes of heap
can be read as a pointer, but very little of it points at something shaped like
a player's name.
*/
func ReadMonoString(mem Mem, ptr uint32) (string, bool) {
	header := mem.Read(ptr, 12)
	if len(header) < 12 {
		return "", false
	}
	length := int32(binary.LittleEndian.Uint32(header[8:12])) //nolint:gosec // a signed field
	if length < 1 || length > 64 {
		return "", false
	}
	raw := mem.Read(ptr+12, int(length)*2)
	if len(raw) < int(length)*2 {
		return "", false
	}
	units := make([]uint16, length)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	text := string(utf16.Decode(units))
	for _, r := range text {
		if r < 32 || r >= 127 {
			return "", false
		}
	}
	return text, true
}

/*
FindPlayers is every validated player copy in writable memory.

Typically the live one plus an inert load-time snapshot or two that share the
character's name. A caller that writes or freezes can act on all of them: the
snapshots ignore what is written to them.
*/
func FindPlayers(mem Mem) []Block {
	var found []Block
	for _, region := range mem.Regions() {
		start := region.Start
		buf := mem.Read(start, region.Size())
		words := len(buf) / 4
		if words < BlockLen {
			continue
		}
		for i := 0; i+BlockLen <= words; i++ {
			// statLife sits at block index 2, so prefilter on the cap and the
			// current value before unpacking the rest: this loop runs over
			// every word of about 1.6 GB.
			lifeMax := word(buf, i+1)
			life := word(buf, i+2)
			if lifeMax < 100 || lifeMax > 500 || life < 1 || life > lifeMax {
				continue
			}
			fields := []int32{
				word(buf, i), lifeMax, life,
				word(buf, i+3), word(buf, i+4), word(buf, i+5),
			}
			if !ValidBlock(fields) {
				continue
			}
			lifeAddr := start + uint32(i+2)*4 //nolint:gosec // an offset inside a 32-bit region
			block, ok := blockAt(mem, lifeAddr, fields)
			if !ok {
				continue
			}
			found = append(found, block)
		}
	}
	return found
}

// word is the i'th little-endian int32 of a buffer.
func word(buf []byte, i int) int32 {
	return int32(binary.LittleEndian.Uint32(buf[i*4:])) //nolint:gosec // a signed field
}

/*
ReadBlock builds a validated Block from a statLife address, or reports that
there is not one there.

For a caller that already knows where a player is -- a cached address being
re-checked -- rather than one scanning for it.
*/
func ReadBlock(mem Mem, lifeAddr uint32) (Block, bool) {
	raw := mem.Read(uint32(int(lifeAddr)+blockStart), BlockLen*4) //nolint:gosec // a 32-bit address
	if len(raw) < BlockLen*4 {
		return Block{}, false
	}
	fields := make([]int32, BlockLen)
	for i := range fields {
		fields[i] = word(raw, i)
	}
	if !ValidBlock(fields) {
		return Block{}, false
	}
	return blockAt(mem, lifeAddr, fields)
}

// blockAt names a validated block by reading the string its name pointer leads
// to. A block with no readable name is not a player.
func blockAt(mem Mem, lifeAddr uint32, fields []int32) (Block, bool) {
	ptr := mem.Read(uint32(int(lifeAddr)+NameOffset), 4) //nolint:gosec // a 32-bit address
	if len(ptr) < 4 {
		return Block{}, false
	}
	name, ok := ReadMonoString(mem, binary.LittleEndian.Uint32(ptr))
	if !ok {
		return Block{}, false
	}
	return Block{
		LifeAddr: lifeAddr, StatLifeMax2: fields[0], StatLifeMax: fields[1],
		StatLife: fields[2], StatMana: fields[3], StatManaMax: fields[4],
		StatManaMax2: fields[5], Name: name,
	}, true
}
