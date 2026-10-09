package locate

import (
	"encoding/binary"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Ground truth under the CLR: which player copy is live, by Main's statics.

Under mono, get_LocalPlayer's JIT code leads to Main.player and Main.myPlayer
(localplayer.go). The CLR compiles that method differently, and its statics live
in a block of their own (spec 052 phase 0), so this finds the block by what it
points to instead:

 1. Some found player copy is an element of Main.player, a Player[] of the
    game's own length.
 2. Exactly one static slot holds that array and also has Main.npc and
    Main.projectile beside it at the measured distances, each an array of the
    game's own length. Three arrays of the right lengths in the right places is
    the block's identity; one alone would be a coincidence sink.
 3. The live player is the array's one active element. In single-player every
    other slot holds an inactive placeholder.

The slot is what is kept between calls. Static storage does not move, but it is
re-validated on every use all the same -- three reads -- and the arrays and
players it leads to are re-read each time, because those the GC does move.
*/

// u32At is the little-endian word at addr, and whether it was readable.
func u32At(mem Mem, addr uint32) (uint32, bool) {
	b := mem.Read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

// arrayLen is the length of the szarray at addr, under this entry's shape.
func (l Locator) arrayLen(mem Mem, addr uint32) (uint32, bool) {
	if addr == 0 {
		return 0, false
	}
	return u32At(mem, addr+uint32(l.arrLen)) //nolint:gosec // a header offset
}

// isPlayerSlot reports whether slot is Main.player's: it leads to a Player[] and
// has the NPC[] and Projectile[] beside it, all of the game's lengths.
func (l Locator) isPlayerSlot(mem Mem, slot uint32) bool {
	s := l.statics
	for _, want := range []struct {
		at  int
		len uint32
	}{{0, s.PlayerLen}, {s.NPCFromPlayer, s.NPCLen}, {s.ProjectileFromPlayer, s.ProjectileLen}} {
		arr, ok := u32At(mem, uint32(int(slot)+want.at)) //nolint:gosec // a 32-bit address
		if !ok {
			return false
		}
		if n, ok := l.arrayLen(mem, arr); !ok || n != want.len {
			return false
		}
	}
	return true
}

/*
scanFor is every 4-aligned address in writable memory whose word is one of the
wanted values, with the value found there.

The wanted values are a few heap addresses, so a word is first compared with
their range: nearly every word in 1.4 GB is outside it, and a map lookup on every
one cost 8.5 seconds on the live game.
*/
func scanFor(mem Mem, want map[uint32]bool) map[uint32]uint32 {
	out := map[uint32]uint32{}
	lo, hi := ^uint32(0), uint32(0)
	for v := range want {
		lo, hi = min(lo, v), max(hi, v)
	}
	for _, r := range mem.Regions() {
		buf := mem.Read(r.Start, r.Size())
		for i := 0; i+4 <= len(buf); i += 4 {
			v := binary.LittleEndian.Uint32(buf[i:])
			if v < lo || v > hi {
				continue
			}
			if want[v] {
				out[r.Start+uint32(i)] = v //nolint:gosec // an offset inside a 32-bit region
			}
		}
	}
	return out
}

/*
FindPlayerSlot is Main.player's static slot, found from player copies a scan
already found, or false when there is not exactly one.

Only for an entry that finds the live player ByStatics.
*/
func (l Locator) FindPlayerSlot(mem Mem, copies []Block) (uint32, bool) {
	if l.localPlayer != layout.ByStatics || len(copies) == 0 {
		return 0, false
	}
	s := l.statics
	objects := map[uint32]bool{}
	for _, c := range copies {
		objects[uint32(int(c.LifeAddr)-s.LifeInPlayer)] = true //nolint:gosec // a 32-bit address
	}
	// Step 1: the Player[] arrays that hold one of the copies.
	arrays := map[uint32]bool{}
	for elem := range scanFor(mem, objects) {
		for k := uint32(0); k < s.PlayerLen; k++ {
			start := elem - uint32(l.arrData) - 4*k //nolint:gosec // a header offset
			if n, ok := l.arrayLen(mem, start); ok && n == s.PlayerLen {
				arrays[start] = true
				break
			}
		}
	}
	if len(arrays) == 0 {
		return 0, false
	}
	// Step 2: the one slot that holds such an array and has the other two
	// beside it.
	var found []uint32
	for slot := range scanFor(mem, arrays) {
		if l.isPlayerSlot(mem, slot) {
			found = append(found, slot)
		}
	}
	if len(found) != 1 {
		return 0, false // none, or a tie: no answer either way
	}
	return found[0], true
}

/*
LiveAt is the live player through Main.player's slot: the array's one active
element, read as a validated block. False when the slot no longer reads as
Main.player's, or when not exactly one player is active.
*/
func (l Locator) LiveAt(mem Mem, slot uint32) (Block, bool) {
	if l.localPlayer != layout.ByStatics || !l.isPlayerSlot(mem, slot) {
		return Block{}, false
	}
	s := l.statics
	arr, _ := u32At(mem, slot)
	elems := mem.Read(arr+uint32(l.arrData), int(s.PlayerLen)*4) //nolint:gosec // a header offset
	if len(elems) < int(s.PlayerLen)*4 {
		return Block{}, false
	}
	var live uint32
	for i := 0; i < int(s.PlayerLen); i++ {
		obj := binary.LittleEndian.Uint32(elems[i*4:])
		if obj == 0 {
			continue
		}
		active := mem.Read(obj+uint32(s.PlayerActive), 1) //nolint:gosec // a field offset
		if len(active) == 1 && active[0] != 0 {
			if live != 0 {
				return Block{}, false // two active players is not single-player
			}
			live = obj
		}
	}
	if live == 0 {
		return Block{}, false
	}
	return l.ReadBlock(mem, live+uint32(s.LifeInPlayer)) //nolint:gosec // a field offset
}

// mainStatics is the CLR's LiveFinder: Main.player's static slot, recognised
// from the copies a scan found.
type mainStatics struct{ l Locator }

func (m mainStatics) Find(mem ExecMem, copies []Block) (uint32, bool) {
	return m.l.FindPlayerSlot(mem, copies)
}
func (m mainStatics) At(mem Mem, slot uint32) (Block, bool) { return m.l.LiveAt(mem, slot) }
