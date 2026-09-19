package content

import (
	"encoding/binary"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Finding the NPC objects, which is where the template scan starts.

Every offset here is a build constant, so each is **checked rather than trusted**
and each has a fallback that scans for the thing by its shape. That is what keeps
them from becoming numbers that silently rot: a rebuild that moves Main's statics
costs a scan rather than a cheat.
*/

// plausible reports whether an address could be an object at all. A static slot
// holding a small number or a sentinel is not a pointer.
func plausible(addr uint32) bool { return addr > 0x10000 && addr < 0xFFFFFFF0 }

/*
FindNPCArray is the address of the game's NPC array.

The pinned offset is tried first and its length checked; if that fails -- a
rebuild moved Main's statics -- the static block is scanned for the only array of
the right length whose elements share a vtable.
*/
func FindNPCArray(mem Mem, staticBase uint32) (uint32, bool) {
	want := int32(layout.MaxNPCs + 1)

	lengthOf := func(ptr uint32) (int32, bool) {
		if !plausible(ptr) {
			return 0, false
		}
		return readI32(mem, ptr+layout.ArrLenOff)
	}

	if arr, ok := readU32(mem, staticBase+layout.MainNPCOff); ok {
		if n, ok := lengthOf(arr); ok && n == want {
			return arr, true
		}
	}

	blob := mem.Read(staticBase, 0x4000)
	for off := 0; off+4 <= len(blob); off += 4 {
		cand := binary.LittleEndian.Uint32(blob[off:])
		if n, ok := lengthOf(cand); !ok || n != want {
			continue
		}
		if _, ok := NPCVTableOf(mem, cand); ok {
			return cand, true
		}
	}
	return 0, false
}

/*
NPCVTableOf is the vtable the elements of an NPC array share.

The game allocates every slot at world load, so this does not depend on anything
being alive; requiring agreement across many elements is what rejects an
unrelated array that happens to be the same length.
*/
func NPCVTableOf(mem Mem, arr uint32) (uint32, bool) {
	first, ok := readU32(mem, arr+layout.ArrDataOff)
	if !ok || first == 0 {
		return 0, false
	}
	vt, ok := readU32(mem, first)
	if !ok || vt == 0 {
		return 0, false
	}
	agree := 0
	for i := range min(layout.MaxNPCs, 32) {
		elem, ok := readU32(mem, arr+layout.ArrDataOff+uint32(i)*4) //nolint:gosec // a slot index
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
func FindNPCVTable(mem Mem, staticBase uint32) (uint32, bool) {
	arr, ok := FindNPCArray(mem, staticBase)
	if !ok {
		return 0, false
	}
	return NPCVTableOf(mem, arr)
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
func FrameCounts(mem Mem, staticBase uint32) map[int]int32 {
	countsAt := func(ptr uint32) map[int]int32 {
		if !plausible(ptr) {
			return nil
		}
		n, ok := readI32(mem, ptr+layout.ArrLenOff)
		if !ok || n < 256 || n > 4000 {
			return nil
		}
		blob := mem.Read(ptr+layout.ArrDataOff, int(n)*4)
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

	if ptr, ok := readU32(mem, staticBase+layout.MainNPCFrameCountOff); ok {
		if got := countsAt(ptr); len(got) > 0 {
			return got
		}
	}
	blob := mem.Read(staticBase, 0x4000)
	for off := 0; off+4 <= len(blob); off += 4 {
		if got := countsAt(binary.LittleEndian.Uint32(blob[off:])); len(got) > 0 {
			return got
		}
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
