package content

import (
	"encoding/binary"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Finding the NPC objects, which is where the template scan starts.

Every offset here is a build constant, so each is **checked rather than trusted**
and, under mono, each has a fallback that scans for the thing by its shape. That
is what keeps them from becoming numbers that silently rot: a rebuild that moves
Main's statics costs a scan rather than a cheat.

Where the array and the frame-count table are differs by runtime, so each way of
finding them is a resolver, named by the entry (spec 052). What an array or a
table has to look like to be believed is the same under both.
*/

// plausible reports whether an address could be an object at all. A static slot
// holding a small number or a sentinel is not a pointer.
func plausible(addr uint32) bool { return addr > 0x10000 && addr < 0xFFFFFFF0 }

/*
npcResolver is one runtime's way to Main.npc and Main.npcFrameCount from the base
the service hands it: Main's static block under mono, Main.player's static slot
under the CLR.
*/
type npcResolver struct {
	array  func(mem Mem, e layout.Entry, base uint32) (uint32, bool)
	frames func(mem Mem, e layout.Entry, base uint32) map[int]int32
}

// npcResolvers are the ways, by the name an entry's NPCShape carries.
var npcResolvers = map[string]npcResolver{
	"mono": {array: monoNPCArray, frames: monoFrameCounts},
	"clr":  {array: clrNPCArray, frames: clrFrameCounts},
}

// arrayLength is the length of the szarray at ptr under an entry's shape, when
// ptr could be an object at all.
func arrayLength(mem Mem, e layout.Entry, ptr uint32) (int32, bool) {
	if !plausible(ptr) {
		return 0, false
	}
	return readI32(mem, ptr+uint32(e.Shapes.ArrLenOff)) //nolint:gosec // a header offset
}

/*
FindNPCArray is the address of the game's NPC array, or nothing under an entry
with no way to find it.
*/
func FindNPCArray(mem Mem, e layout.Entry, base uint32) (uint32, bool) {
	resolve, ok := npcResolvers[e.NPC.Resolver]
	if !ok {
		return 0, false
	}
	return resolve.array(mem, e, base)
}

/*
monoNPCArray finds the array through Main's static block.

The pinned offset is tried first and its length checked; if that fails -- a
rebuild moved Main's statics -- the static block is scanned for the only array of
the right length whose elements share a vtable.
*/
func monoNPCArray(mem Mem, e layout.Entry, staticBase uint32) (uint32, bool) {
	want := int32(layout.MaxNPCs + 1)

	if arr, ok := readU32(mem, staticBase+layout.MainNPCOff); ok {
		if n, ok := arrayLength(mem, e, arr); ok && n == want {
			return arr, true
		}
	}

	blob := mem.Read(staticBase, 0x4000)
	for off := 0; off+4 <= len(blob); off += 4 {
		cand := binary.LittleEndian.Uint32(blob[off:])
		if n, ok := arrayLength(mem, e, cand); !ok || n != want {
			continue
		}
		if _, ok := NPCVTableOf(mem, e, cand); ok {
			return cand, true
		}
	}
	return 0, false
}

/*
clrNPCArray finds the array through Main.npc's reference static, a slot at a
measured distance from Main.player's. There is no block to fall back to scanning:
the slot either holds an array of the game's own length whose elements share a
MethodTable, or there is no answer.
*/
func clrNPCArray(mem Mem, e layout.Entry, playerSlot uint32) (uint32, bool) {
	arr, ok := readU32(mem, uint32(int(playerSlot)+e.Statics.NPCFromPlayer)) //nolint:gosec // a slot near the player's
	if !ok {
		return 0, false
	}
	if n, ok := arrayLength(mem, e, arr); !ok || n != int32(layout.MaxNPCs+1) {
		return 0, false
	}
	if _, ok := NPCVTableOf(mem, e, arr); !ok {
		return 0, false
	}
	return arr, true
}

/*
NPCVTableOf is the vtable the elements of an NPC array share -- the MethodTable,
under the CLR; either way the word an object starts with.

The game allocates every slot at world load, so this does not depend on anything
being alive; requiring agreement across many elements is what rejects an
unrelated array that happens to be the same length.
*/
func NPCVTableOf(mem Mem, e layout.Entry, arr uint32) (uint32, bool) {
	data := arr + uint32(e.Shapes.ArrDataOff) //nolint:gosec // a header offset
	first, ok := readU32(mem, data)
	if !ok || first == 0 {
		return 0, false
	}
	vt, ok := readU32(mem, first)
	if !ok || vt == 0 {
		return 0, false
	}
	agree := 0
	for i := range min(layout.MaxNPCs, 32) {
		elem, ok := readU32(mem, data+uint32(i)*4) //nolint:gosec // a slot index
		if !ok || elem == 0 {
			continue
		}
		if got, ok := readU32(mem, elem); ok && got == vt {
			agree++
		}
	}
	if agree < 24 {
		return 0, false
	}
	return vt, true
}

// FindNPCVTable is the shared vtable of NPC objects, which is the entry point
// for the template scan.
func FindNPCVTable(mem Mem, e layout.Entry, base uint32) (uint32, bool) {
	arr, ok := FindNPCArray(mem, e, base)
	if !ok {
		return 0, false
	}
	return NPCVTableOf(mem, e, arr)
}

/*
FrameCounts is how many animation frames each NPC type's sprite sheet holds.

The sheets are vertical strips of equal frames, so this is the only exact way to
crop one to its first frame -- guessing from the shape gets the wide ones wrong.

Validated the same way the array is, and for the same reason: the offset is a
build constant. A plausible table is one whose length covers the NPC types and
whose values are all small counts, with at least one above a single frame -- an
array of ones is some other array.
*/
func FrameCounts(mem Mem, e layout.Entry, base uint32) map[int]int32 {
	resolve, ok := npcResolvers[e.NPC.Resolver]
	if !ok {
		return map[int]int32{}
	}
	return resolve.frames(mem, e, base)
}

// frameCountsAt is the table at ptr, or nil when what is there is not one.
func frameCountsAt(mem Mem, e layout.Entry, ptr uint32) map[int]int32 {
	n, ok := arrayLength(mem, e, ptr)
	if !ok || n < 256 || n > 4000 {
		return nil
	}
	blob := mem.Read(ptr+uint32(e.Shapes.ArrDataOff), int(n)*4) //nolint:gosec // a header offset
	if len(blob) < int(n)*4 {
		return nil
	}
	out := map[int]int32{}
	interesting := false
	for i := range int(n) {
		v := int32(binary.LittleEndian.Uint32(blob[i*4:])) //nolint:gosec // a count, as its bits
		if v < 0 || v > layout.MaxNPCFrames {
			return nil
		}
		if v > 1 {
			interesting = true
		}
		if v > 0 {
			out[i] = v
		}
	}
	if !interesting {
		return nil
	}
	return out
}

// monoFrameCounts tries the pinned offset in Main's static block, then scans the
// block for the only table that reads as one.
func monoFrameCounts(mem Mem, e layout.Entry, staticBase uint32) map[int]int32 {
	if ptr, ok := readU32(mem, staticBase+layout.MainNPCFrameCountOff); ok {
		if got := frameCountsAt(mem, e, ptr); len(got) > 0 {
			return got
		}
	}
	blob := mem.Read(staticBase, 0x4000)
	for off := 0; off+4 <= len(blob); off += 4 {
		if got := frameCountsAt(mem, e, binary.LittleEndian.Uint32(blob[off:])); len(got) > 0 {
			return got
		}
	}
	return map[int]int32{}
}

// clrFrameCounts reads Main.npcFrameCount's reference static, a slot at a
// measured distance from Main.player's, and believes it only if it reads as the
// table.
func clrFrameCounts(mem Mem, e layout.Entry, playerSlot uint32) map[int]int32 {
	ptr, ok := readU32(mem, uint32(int(playerSlot)+e.Statics.NPCFrameCountFromPlayer)) //nolint:gosec // a slot near the player's
	if !ok {
		return map[int]int32{}
	}
	if got := frameCountsAt(mem, e, ptr); len(got) > 0 {
		return got
	}
	return map[int]int32{}
}

// readU32 is one little-endian word, and whether it was readable.
func readU32(mem Mem, addr uint32) (uint32, bool) {
	b := mem.Read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

// readI32 is the same word read as the signed field most of the game's are.
func readI32(mem Mem, addr uint32) (int32, bool) {
	v, ok := readU32(mem, addr)
	return int32(v), ok //nolint:gosec // a word, as its bits
}
