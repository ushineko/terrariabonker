package patch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The arena is memory of this program's own inside the game: read, write and
execute, 64 KB, nobody else's.

Code caves are *borrowed* padding inside somebody else's read-execute mapping,
and this project has hit every limit of that -- a stub too big for a gap, a stub
that needed to write and faulted because the cave was a code section of a DLL,
and a queue that fits in no gap at all. The arena is what a cave search has
always been pointing at.

64 KB because that is VirtualAlloc's reservation granularity, so a smaller
request reserves this much anyway. It is laid out so that every stub's address is
decided by *which injection it belongs to* rather than by searching:

	0x0000..0x0FFF   reserved; the ore extractor's queue lives in here
	0x1000..         one slot per injection site, indexed, never searched for
	ARENA_SIZE-16    the stamp that lets a later run find this arena again

Searching is what went wrong before. A scan for cold bytes cannot tell free space
from a stub that has been disabled and scrubbed, so it handed one injection a
slice of another. An index cannot collide.
*/
const (
	ArenaSize     = 0x10000
	ArenaStubsOff = 0x1000 // the first slot
	ArenaSlot     = 256    // per site; the largest stub today is 96 bytes
	ArenaMaxSites = 8      // per injection; the loot hook has four twins
	ArenaMagicOff = ArenaSize - 16
)

/*
ArenaMagic is stamped at the arena's tail so a later run can find it again.

An arena outlives the process that asked for it -- the memory belongs to the
game, not to the trainer -- so losing the state file must not cost the player
another swing of the bootstrap.

The number was bumped when the slot ordering was fixed: an arena stamped
TBARENA1 was laid out by the old sorted numbering, so adopting one would put
today's stubs on top of yesterday's live ones. A new stamp means such an arena is
ignored and a fresh one allocated, which costs 64 KB and avoids overwriting
running code.
*/
var ArenaMagic = []byte("TBARENA2")

/*
SlotOrder decides where each injection's stub goes, and is APPEND-ONLY.

Never sort it, never reorder it, never remove a name. A slot's address is decided
by position here, and an arena outlives the trainer process that made it.

Slots were indexed by sorted name until the day an injection was added whose name
sorted first: every other slot shifted by 0x800 and the new stub was written
straight over a live one -- inventory_accs, enabled, being jumped into every
frame. The game ran on with corrupted control flow and died a few frames later
somewhere unrelated. Appending is what makes adding an injection safe.
*/
var SlotOrder = []string{
	"inventory_accs", "loot", "ore_extract", "pickup", "smart_cursor",
	"spawn_rate", "teleport", "tool_reach", "vanity_accs",
	"auto_use",
}

// ArenaMem is the memory an arena is found in and written to.
type ArenaMem interface {
	Mem
	AllRegions() []proc.Region
	Write(addr uint32, data []byte) bool
}

/*
SlotFor is where an injection's stub lives in the arena.

Decided by name and site index rather than by searching, so it is the same
address every time: enabling, disabling and re-enabling all land on the same
bytes.
*/
func SlotFor(arena uint32, name string, site int) (uint32, error) {
	index := -1
	for i, n := range SlotOrder {
		if n == name {
			index = i
			break
		}
	}
	if index < 0 {
		return 0, fmt.Errorf("%q has no arena slot: append it to SlotOrder "+
			"(never reorder that list -- see the note there)", name)
	}
	if site < 0 || site >= ArenaMaxSites {
		return 0, fmt.Errorf("%s: site %d is past ArenaMaxSites", name, site)
	}
	off := ArenaStubsOff + (index*ArenaMaxSites+site)*ArenaSlot
	if off+ArenaSlot > ArenaMagicOff {
		return 0, fmt.Errorf("%s: arena slot %d runs past the arena", name, index)
	}
	return arena + uint32(off), nil //nolint:gosec // an offset inside 64 KB
}

/*
FreeBase is a free 64 KB-aligned address with room for an arena.

Chosen per session and never hardcoded: the map differs every launch, and the
obvious round numbers sit inside mono's big executable arenas -- 0x30000000
turned out to be 33 MB into one.
*/
func FreeBase(mem ArenaMem, need int) (uint32, error) {
	regions := mem.AllRegions()
	sort.Slice(regions, func(i, j int) bool { return regions[i].Start < regions[j].Start })

	var merged []proc.Region
	for _, r := range regions {
		if n := len(merged); n > 0 && r.Start <= merged[n-1].End {
			if r.End > merged[n-1].End {
				merged[n-1].End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}

	room := uint32(need) //nolint:gosec // the arena size
	if room < 0x100000 {
		room = 0x100000
	}
	for i := 0; i+1 < len(merged); i++ {
		end, next := merged[i].End, merged[i+1].Start
		base := (end + 0xFFFF) &^ 0xFFFF
		// Only gaps above 0x10000000 are considered, as the Python does: the
		// low end of the map is where the game's image and its libraries sit.
		// The `next > base` is this port's own: the rounding up can carry base
		// past the next mapping, and unsigned arithmetic would wrap that into a
		// gap the size of the address space.
		if end >= 0x10000000 && next > base && next-base >= room {
			return base, nil
		}
	}
	return 0, fmt.Errorf("no free 64KB-aligned hole for an arena")
}

// Mapped is the region containing an address, and whether there is one.
func Mapped(mem ArenaMem, addr uint32) (proc.Region, bool) {
	for _, r := range mem.AllRegions() {
		if r.Start <= addr && addr < r.End {
			return r, true
		}
	}
	return proc.Region{}, false
}

/*
ArenaOK reports whether an address is still an arena of this program's.

It checks the stamp and not just the map: a plain address could be anything by
the time it is looked at again, and the whole point of the stamp is that memory
handed back by the game is indistinguishable from memory handed to somebody else.
*/
func ArenaOK(mem ArenaMem, base uint32) bool {
	if _, ok := Mapped(mem, base); !ok {
		return false
	}
	return bytes.Equal(mem.Read(base+ArenaMagicOff, len(ArenaMagic)), ArenaMagic)
}

// FindArena is an arena this process was already given, found by its stamp.
func FindArena(mem ArenaMem) (uint32, bool) {
	for _, r := range mem.AllRegions() {
		if r.End-r.Start != ArenaSize || !r.Writable || !r.Executable {
			continue
		}
		if ArenaOK(mem, r.Start) {
			return r.Start, true
		}
	}
	return 0, false
}

/*
FindCave is size bytes of executable padding for a stub: space *borrowed* rather
than allocated.

It looks for a run of int3 (0xCC, preferred) or zero, which is the alignment
padding the JIT emits between methods. int3 because it traps if it is ever
executed, so a long run of it is almost certainly unreachable filler; zero is the
weaker fallback, since it decodes as a real instruction and a long zero run could
be live data.

Why borrowing is safe *enough*: mono JITs lazily into code-manager chunks with a
bump allocator, so alignment padding inside an already-emitted chunk is not put
back on any free list and will not be handed to a later method. wine-mono is the
old non-tiered runtime -- no re-JIT, effectively no code unloading -- so the
borrowed bytes are stable for the life of the process, and JIT churn grows the
pool forward rather than into them.

It is still a heuristic. The failure is a clobbered stub and a crash, which is
cheaply recoverable: disabling restores the site, a restart clears everything,
and the anchors are re-derived every session.

**Risk scales with size.** A real alignment gap is small; a large request can only
be met by a long cold run, which is both rarer and more likely to be actual data.
This is a small-stub-only technique, and the answer to outgrowing it is to
allocate -- which is what the arena is.

writable is for a stub whose own code writes inside its cave. Almost every
executable mapping here is read-execute, so such a stub faults on its first run
even though installing it succeeded: /proc/pid/mem ignores page protection and
the CPU does not. Asking for a writable cave usually finds nothing, which is the
honest answer.
*/
func FindCave(sc *Scanner, size int, claimed []uint32, writable bool, state State) (uint32, error) {
	want := size + 4
	busy := state.InstalledCaves()
	for _, c := range claimed {
		busy = append(busy, [2]uint32{c, uint32(size)}) //nolint:gosec // a stub length
	}

	for _, pad := range []byte{0xCC, 0x00} {
		needle := bytes.Repeat([]byte{pad}, want)
		for _, r := range sc.Regions(writable) {
			buf := sc.Mem.Read(r.Start, r.Size())
			for i := 0; ; {
				at := bytes.Index(buf[i:], needle)
				if at < 0 {
					break
				}
				at += i
				// Two bytes into the run, as a small margin.
				cave := r.Start + uint32(at) + 2            //nolint:gosec // an offset in a 32-bit region
				if !overlapsAny(cave, uint32(size), busy) { //nolint:gosec // a stub length
					return cave, nil
				}
				i = at + 1
			}
		}
	}
	return 0, fmt.Errorf("no code cave found for the injection stub")
}

/*
overlapsAny reports whether a proposed cave runs into anything already taken.

Handing out occupied space writes one stub over another, and what runs afterwards
is the splice: a corrupted register, then a crash somewhere else entirely.
*/
func overlapsAny(cave, size uint32, busy [][2]uint32) bool {
	for _, b := range busy {
		if cave < b[0]+b[1] && b[0] < cave+size {
			return true
		}
	}
	return false
}

/*
Rel32 encodes the displacement for a five-byte jump at src-5 reaching target.

Packed unsigned, as two's complement, so it is right whichever way the jump goes.
*/
func Rel32(srcAfter, target uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, target-srcAfter)
}

/*
CheckSite refuses to write a jump over bytes that are not what will be put back.

A five-byte jump goes over live code and the original is replayed from the
recorded bytes. If the address is wrong -- an off-by-offset, a stale record from a
previous process -- then both halves are wrong: the jump lands mid-instruction
and the restore leaves those bytes somewhere they never belonged. That is not a
crash anybody can diagnose afterwards, because the evidence is the corruption.

Checking costs one read. It caught nothing for a year and then caught a jump
written 0x15 bytes early into Player.Update's dead-check, which killed the game
on the next frame.
*/
func CheckSite(mem ArenaMem, site uint32, expect []byte, what string) error {
	found := mem.Read(site, len(expect))
	if !bytes.Equal(found, expect) {
		return fmt.Errorf("%s: bytes at %#08X are % X, expected % X -- refusing to patch "+
			"an address that is not the site it was resolved for", what, site, found, expect)
	}
	return nil
}

/*
CheckSlot refuses to write a stub into an arena slot that already holds one.

A free slot is zeros, from a fresh allocation, or 0xCC, scrubbed by a disable.
Anything else is somebody's live code, and writing over it redirects their site's
jump into this one -- which does not fault. It runs the wrong instructions and
returns to the wrong method, and the game dies later, somewhere unrelated, with a
stack that names nothing to do with the trainer.

That is not hypothetical: the auto-use stub once went over a running
inventory_accs stub because slots were indexed by sorted name and a new injection
sorted first. SlotOrder makes that particular mistake impossible; this refuses the
*next* way of arriving at the same place.
*/
func CheckSlot(mem ArenaMem, cave uint32, length int, what string) error {
	got := mem.Read(cave, length)
	if len(got) < length {
		return fmt.Errorf("%s: arena slot at %#x is not readable", what, cave)
	}
	for _, b := range got {
		if b != 0x00 && b != 0xCC {
			head := got
			if len(head) > 8 {
				head = head[:8]
			}
			return fmt.Errorf("%s: arena slot at %#x already holds code (%x...). "+
				"Refusing to write over a stub that may be live", what, cave, head)
		}
	}
	return nil
}
