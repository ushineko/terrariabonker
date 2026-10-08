//go:build windows

/*
Command clrfields asks the .NET Framework runtime for field offsets by name --
the Windows counterpart of cmd/monofields (spec 052, phase 2).

The CLR keeps one FieldDesc per stored field, in a contiguous list per class.
On 32-bit .NET Framework the model is

	struct FieldDesc {
		MethodTable *enclosing;   // +0
		DWORD mb;                 // +4: RID in bits 0..16 (packed layout), isStatic bit 24
		DWORD off;                // +8: offset in bits 0..26, CorElementType in 27..31
	}

with instance offsets counted from the end of the 4-byte MethodTable pointer.
Names are not in memory; they come from the assembly's metadata by RID. A run
of records that share one enclosing pointer and carry the RIDs of one TypeDef's
fields is that class's list. The isStatic bit is checked against the
metadata's own static flag, so a wrong model shows up as disagreement rather
than as plausible numbers.

	clrfields Terraria.Player Terraria.Entity

Read-only. It never writes to the game.
*/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	memPrivate     = 0x20000
	ridMask        = 0x1FFFF
	fullMbBit      = 1 << 30
	staticBit      = 1 << 24
	offMask        = 0x7FFFFFF
	objectHeader   = 4 // the MethodTable pointer instance offsets are counted after
	fieldDescBytes = 12
)

type owner struct {
	typ   int
	field int
}

type record struct {
	hi, et2  uint32
	mt, rid  uint32
	static   bool
	off      uint32
	elemType uint32
}

func main() {
	stride := flag.Int("stride", fieldDescBytes, "record size for -raw")
	fragments := flag.Bool("fragments", false, "also print runs shorter than the class's stored-field count")
	bits := flag.Bool("bits", false, "cross-tabulate each record's high flag byte against the metadata")
	find := flag.String("find", "", "look for consecutive RIDs of this type at any stride, then exit")
	raw := flag.Int("raw", 0, "print runs of at least this many records sharing a first dword, then exit")
	exe := flag.String("exe", `C:\Program Files (x86)\Steam\steamapps\common\Terraria\Terraria.exe`, "the game assembly")
	flag.Parse()
	types, err := LoadTypes(*exe)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	want := map[string]bool{}
	for _, a := range flag.Args() {
		want[a] = true
	}
	if len(want) == 0 {
		fmt.Fprintln(os.Stderr, "usage: clrfields [-exe path] Namespace.Type ...")
		os.Exit(2)
	}
	byRID := map[uint32]owner{}
	for ti, t := range types {
		for fi, f := range t.Fields {
			byRID[f.RID] = owner{ti, fi}
		}
	}
	h, err := open()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *find != "" {
		for _, t := range types {
			if t.FullName() == *find {
				findSequential(h, t)
			}
		}
		return
	}
	if *raw > 0 {
		rawRuns(h, *raw, *stride)
		return
	}
	runs := scan(h, byRID)

	for ti, t := range types {
		if !want[t.FullName()] {
			continue
		}
		delete(want, t.FullName())
		stored := 0
		for _, f := range t.Fields {
			if !f.Literal {
				stored++
			}
		}
		var complete [][]record
		for _, run := range runs[ti] {
			// A class's list holds one record per stored field. A shorter run is
			// a coincidence of RIDs elsewhere in memory, kept only on request.
			if len(run) == stored || *fragments {
				complete = append(complete, run)
			}
		}
		found := complete
		fmt.Printf("== %s: %d fields declared, %d stored, %d complete FieldDesc list(s)\n",
			t.FullName(), len(t.Fields), stored, len(found))
		for _, run := range found {
			agree, disagree := 0, 0
			sort.Slice(run, func(i, j int) bool {
				if run[i].static != run[j].static {
					return !run[i].static
				}
				return run[i].off < run[j].off
			})
			for _, r := range run {
				f := t.Fields[byRID[r.rid].field]
				if f.Static == r.static {
					agree++
				} else {
					disagree++
				}
				kind, shown := "inst", r.off+objectHeader
				if r.static {
					kind, shown = "static", r.off
				}
				fmt.Printf("  %-6s %#05x  et=%#02x  %s\n", kind, shown, r.elemType, f.Name)
			}
			if *bits {
				tab := map[string]int{}
				for _, r := range run {
					f := t.Fields[byRID[r.rid].field]
					tab[fmt.Sprintf("hi=%02x static=%v access=%d", r.hi, f.Static, f.Flags&7)]++
				}
				for k, v := range tab {
					fmt.Println("   ", k, v)
				}
			}
			fmt.Printf("  MethodTable %#08x, %d records; isStatic agrees with metadata %d/%d\n",
				run[0].mt, len(run), agree, agree+disagree)
		}
	}
	for name := range want {
		fmt.Printf("== %s: no such TypeDef in the assembly\n", name)
	}
}

func open() (windows.Handle, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("listing processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "Terraria.exe") {
			h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pe.ProcessID)
			if err != nil {
				return 0, fmt.Errorf("opening the game (pid %d) to read: %w", pe.ProcessID, err)
			}
			return h, nil
		}
	}
	return 0, fmt.Errorf("the game is not running (no Terraria.exe process)")
}

// scan finds every run of FieldDesc-shaped records whose RIDs all belong to
// one TypeDef, keyed by that TypeDef's index.
func scan(h windows.Handle, byRID map[uint32]owner) map[int][][]record {
	out := map[int][][]record{}
	decode := func(at uint32, b []byte) (record, int, bool) {
		// The enclosing MethodTable is self-relative: the record's own
		// address plus the signed dword it starts with.
		mt := at + binary.LittleEndian.Uint32(b)
		mb := binary.LittleEndian.Uint32(b[4:])
		if mt < 0x10000 || mt == at {
			return record{}, 0, false
		}
		o, ok := byRID[mb&ridMask]
		if !ok || mb&ridMask == 0 {
			return record{}, 0, false
		}
		d2 := binary.LittleEndian.Uint32(b[8:])
		return record{mt: mt, rid: mb & ridMask, hi: mb >> 24, et2: d2, static: mb&staticBit != 0,
			off: d2 & offMask, elemType: d2 >> 27}, o.typ, true
	}
	var addr uintptr
	for addr < 0xFFFF0000 {
		var mbi windows.MemoryBasicInformation
		if windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)) != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		readable := mbi.Protect&(windows.PAGE_READWRITE|windows.PAGE_READONLY|windows.PAGE_EXECUTE_READWRITE) != 0
		if mbi.State == windows.MEM_COMMIT && mbi.Type == memPrivate && readable && mbi.Protect&windows.PAGE_GUARD == 0 {
			buf := make([]byte, mbi.RegionSize)
			var got uintptr
			_ = windows.ReadProcessMemory(h, mbi.BaseAddress, &buf[0], mbi.RegionSize, &got)
			buf = buf[:got]
			for i := 0; i+2*fieldDescBytes <= len(buf); i += 4 {
				first, typ, ok := decode(uint32(mbi.BaseAddress)+uint32(i), buf[i:]) //nolint:gosec // a 32-bit process's address
				if !ok {
					continue
				}
				run := []record{first}
				seen := map[uint32]bool{first.rid: true}
				j := i + fieldDescBytes
				for ; j+fieldDescBytes <= len(buf); j += fieldDescBytes {
					r, t, ok := decode(uint32(mbi.BaseAddress)+uint32(j), buf[j:]) //nolint:gosec // a 32-bit process's address
					if !ok || t != typ || r.mt != first.mt || seen[r.rid] {
						break
					}
					seen[r.rid] = true
					run = append(run, r)
				}
				if len(run) >= 2 {
					out[typ] = append(out[typ], run)
					i = j - 4
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	return out
}
