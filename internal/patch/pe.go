package patch

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

/*
Finding a Windows function in the running game.

wine maps its DLLs anonymously, so the process map cannot name them: a mapping is
just an address range with no file behind it. The name has to come out of the
image itself, which means walking the PE header of every readable mapping and
asking each one what it is.

Only the arena bootstrap needs this, to call VirtualAlloc, but it needs it before
anything else can work.
*/

// Exports is a PE image's name: what it calls itself, and what it offers.
type Exports struct {
	Module string
	Funcs  map[string]uint32
}

/*
PEExports reads the export directory of an image mapped at base.

Reports nothing for anything that is not a PE with exports, which is almost every
mapping it is handed -- that is the normal case, not a failure.
*/
func PEExports(mem Mem, base uint32) (Exports, bool) {
	head := mem.Read(base, 0x40)
	if len(head) < 0x40 || head[0] != 'M' || head[1] != 'Z' {
		return Exports{}, false
	}
	lfa := binary.LittleEndian.Uint32(head[0x3C:])
	if lfa > 0x400 {
		return Exports{}, false
	}
	pe := mem.Read(base+lfa, 0x100)
	if len(pe) < 0x100 || string(pe[:4]) != "PE\x00\x00" {
		return Exports{}, false
	}
	expRVA := binary.LittleEndian.Uint32(pe[0x78:]) // the first data directory
	if expRVA == 0 {
		return Exports{}, false
	}
	dir := mem.Read(base+expRVA, 0x28)
	if len(dir) < 0x28 {
		return Exports{}, false
	}
	var (
		nameRVA  = binary.LittleEndian.Uint32(dir[0x0C:])
		numNames = binary.LittleEndian.Uint32(dir[0x18:])
		funcRVA  = binary.LittleEndian.Uint32(dir[0x1C:])
		namesRVA = binary.LittleEndian.Uint32(dir[0x20:])
		ordRVA   = binary.LittleEndian.Uint32(dir[0x24:])
	)
	out := Exports{Module: cstring(mem.Read(base+nameRVA, 64)), Funcs: map[string]uint32{}}
	// A count this large is a header that has been read wrong, not an image with
	// that many exports.
	if numNames == 0 || numNames > 20000 {
		return out, out.Module != ""
	}

	names := mem.Read(base+namesRVA, int(4*numNames))
	ords := mem.Read(base+ordRVA, int(2*numNames))
	if len(names) < int(4*numNames) || len(ords) < int(2*numNames) {
		return out, out.Module != ""
	}
	var highest uint16
	for i := range numNames {
		if o := binary.LittleEndian.Uint16(ords[2*i:]); o > highest {
			highest = o
		}
	}
	blob := mem.Read(base+funcRVA, 4*(int(highest)+1))
	if len(blob) < 4*(int(highest)+1) {
		return out, out.Module != ""
	}
	for i := range numNames {
		name := cstring(mem.Read(base+binary.LittleEndian.Uint32(names[4*i:]), 96))
		ord := binary.LittleEndian.Uint16(ords[2*i:])
		out.Funcs[name] = base + binary.LittleEndian.Uint32(blob[4*ord:])
	}
	return out, true
}

// cstring is a NUL-terminated name as the image spells it.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// ResolveExport is the address of a function in the running process, found by
// asking every readable image what it is.
func ResolveExport(mem ArenaMem, module, fn string) (uint32, error) {
	seen := map[uint32]bool{}
	var bases []uint32
	for _, r := range mem.AllRegions() {
		if !r.Readable || seen[r.Start] {
			continue
		}
		seen[r.Start] = true
		bases = append(bases, r.Start)
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })

	want := strings.ToLower(module)
	for _, base := range bases {
		exp, ok := PEExports(mem, base)
		if !ok || !strings.Contains(strings.ToLower(exp.Module), want) {
			continue
		}
		if addr, ok := exp.Funcs[fn]; ok {
			return addr, nil
		}
	}
	return 0, fmt.Errorf("%s!%s not found in the running process", module, fn)
}
