//go:build windows

/*
Command winrecon is a read-only probe of a natively running Windows Terraria:
which runtime is executing it, whether the Linux build's player scan finds the
player, where the name and inventory pointers sit relative to statLife, and
whether the code-patch anchors match the JIT output.

Recon only (spec 052). It opens the game with PROCESS_VM_READ and never writes.
*/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
)

type region struct{ start, end uint32 }

// memPrivate is MEM_PRIVATE, which x/sys/windows does not export.
const memPrivate = 0x20000

type game struct {
	h windows.Handle
}

func (g game) read(addr uint32, n int) []byte {
	buf := make([]byte, n)
	var got uintptr
	err := windows.ReadProcessMemory(g.h, uintptr(addr), &buf[0], uintptr(n), &got)
	if err != nil && got == 0 {
		return nil
	}
	return buf[:got]
}

func (g game) u32(addr uint32) (uint32, bool) {
	b := g.read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

func findPID() uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "Terraria.exe") {
			return pe.ProcessID
		}
	}
	return 0
}

func modules(pid uint32) []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return []string{"(module snapshot failed: " + err.Error() + ")"}
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var me windows.ModuleEntry32
	me.Size = uint32(unsafe.Sizeof(me))
	var out []string
	for err = windows.Module32First(snap, &me); err == nil; err = windows.Module32Next(snap, &me) {
		out = append(out, fmt.Sprintf("%08x %s", me.ModBaseAddr, windows.UTF16ToString(me.ExePath[:])))
	}
	return out
}

// regions is committed memory, split by whether it is writable data or code.
func (g game) regions() (data, exec []region) {
	var addr uintptr
	for addr < 0xFFFF0000 {
		var mbi windows.MemoryBasicInformation
		if err := windows.VirtualQueryEx(g.h, addr, &mbi, unsafe.Sizeof(mbi)); err != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if mbi.State == windows.MEM_COMMIT && mbi.Protect&windows.PAGE_GUARD == 0 {
			r := region{uint32(mbi.BaseAddress), uint32(next)} //nolint:gosec // a 32-bit process's address
			switch mbi.Protect &^ (windows.PAGE_NOCACHE | windows.PAGE_WRITECOMBINE) {
			case windows.PAGE_READWRITE:
				if mbi.Type == memPrivate {
					data = append(data, r)
				}
			case windows.PAGE_EXECUTE_READWRITE, windows.PAGE_EXECUTE_READ, windows.PAGE_EXECUTE_WRITECOPY:
				exec = append(exec, r)
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	return data, exec
}

// clrString decodes a 32-bit CLR System.String: MethodTable, int32 length,
// then UTF-16 at +8.
func (g game) clrString(ptr uint32) (string, uint32, bool) {
	h := g.read(ptr, 8)
	if len(h) < 8 {
		return "", 0, false
	}
	n := int(int32(binary.LittleEndian.Uint32(h[4:]))) //nolint:gosec // a length, as its bits
	if n < 1 || n > 32 {
		return "", 0, false
	}
	b := g.read(ptr+8, 2*n)
	if len(b) < 2*n {
		return "", 0, false
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
		if u[i] < 0x20 || u[i] > 0x7E {
			return "", 0, false
		}
	}
	return string(utf16.Decode(u)), binary.LittleEndian.Uint32(h), true
}

type hit struct {
	life   uint32
	fields []int32
}

func (g game) scanPlayers(data []region) []hit {
	var out []hit
	const chunk = 1 << 20
	for _, r := range data {
		for base := r.start; base < r.end; base += chunk {
			n := chunk
			if rem := int(r.end - base); rem < n {
				n = rem
			}
			buf := g.read(base, n+24)
			for i := 0; i+24 <= len(buf); i += 4 {
				v := make([]int32, 6)
				for k := range v {
					v[k] = int32(binary.LittleEndian.Uint32(buf[i+4*k:])) //nolint:gosec // a field, as its bits
				}
				if validBlock(v) {
					out = append(out, hit{base + uint32(i) + 8, v})
				}
			}
		}
	}
	return out
}

// anchors reads rawAnchors out of internal/patch/anchors.go, so the probe
// tests the exact strings the Linux build ships.
func anchors(path string) map[string]string {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, nil, 0)
	if err != nil {
		fmt.Println("anchors:", err)
		return nil
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "rawAnchors" {
			return true
		}
		for _, el := range vs.Values[0].(*ast.CompositeLit).Elts {
			kv := el.(*ast.KeyValueExpr)
			name, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
			call, ok := kv.Value.(*ast.CallExpr)
			if !ok {
				continue
			}
			out[name] = concat(call.Args[0])
		}
		return false
	})
	return out
}

func concat(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		s, _ := strconv.Unquote(x.Value)
		return s
	case *ast.BinaryExpr:
		return concat(x.X) + concat(x.Y)
	case *ast.ParenExpr:
		return concat(x.X)
	}
	return ""
}

func matchCount(g game, exec []region, pat string) int {
	var want []int // -1 = wildcard
	for _, tok := range strings.Fields(pat) {
		if tok == "??" {
			want = append(want, -1)
			continue
		}
		b, _ := strconv.ParseUint(tok, 16, 8)
		want = append(want, int(b))
	}
	count := 0
	for _, r := range exec {
		buf := g.read(r.start, int(r.end-r.start))
	scan:
		for i := 0; i+len(want) <= len(buf); i++ {
			for k, w := range want {
				if w >= 0 && int(buf[i+k]) != w {
					continue scan
				}
			}
			count++
		}
	}
	return count
}

var statics = flag.Uint("statics", 0, "locate Main's static blocks from this live Player object address, then exit")
var peek = flag.String("peekf64", "", "comma-separated hex addresses: read a double at each twice, a second apart, then exit")
var aob = flag.String("aob", "", "only search executable memory for this pattern and print each site")

func main() {
	flag.Parse()
	pid := findPID()
	if pid == 0 {
		fmt.Println("Terraria.exe is not running")
		os.Exit(1)
	}
	h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		fmt.Println("OpenProcess:", err)
		os.Exit(1)
	}
	g := game{h}
	var wow bool
	_ = windows.IsWow64Process(h, &wow)
	fmt.Printf("pid %d  wow64(32-bit)=%v  (opened without elevation)\n", pid, wow)

	fmt.Println("\n== runtime modules ==")
	for _, m := range modules(pid) {
		l := strings.ToLower(m)
		for _, k := range []string{"clr.dll", "clrjit", "mscorwks", "mono", "coreclr", "terraria.exe", "mscorlib"} {
			if strings.Contains(l, k) {
				fmt.Println(" ", m)
				break
			}
		}
	}

	data, exec := g.regions()
	if *peek != "" {
		peekF64(g, *peek)
		return
	}
	if *statics != 0 {
		clrStatics(g, data, uint32(*statics)) //nolint:gosec // a 32-bit address from the command line
		return
	}
	if *aob != "" {
		aobSites(g, exec, *aob, 20)
		return
	}
	var dsz, esz uint64
	for _, r := range data {
		dsz += uint64(r.end - r.start)
	}
	for _, r := range exec {
		esz += uint64(r.end - r.start)
	}
	fmt.Printf("\n== regions ==\n  rw private: %d regions, %d MB;  exec: %d regions, %d MB\n",
		len(data), dsz>>20, len(exec), esz>>20)

	fmt.Println("\n== player scan (locate.ValidBlock) ==")
	hits := g.scanPlayers(data)
	fmt.Printf("  %d six-int blocks pass ValidBlock\n", len(hits))
	nameAt := map[int]int{}
	type named struct {
		h    hit
		off  int
		name string
	}
	var withName []named
	for _, h := range hits {
		// Mono offset first, as the Linux build reads it.
		if p, ok := g.u32(uint32(int64(h.life) + layout.NamePtrOff)); ok { //nolint:gosec // a delta from statLife
			if s, _, ok := g.clrString(p); ok {
				nameAt[layout.NamePtrOff]++
				withName = append(withName, named{h, layout.NamePtrOff, s})
				continue
			}
		}
		for off := -0x1000; off <= 0x400; off += 4 {
			p, ok := g.u32(uint32(int64(h.life) + int64(off))) //nolint:gosec // a delta from statLife
			if !ok || p < 0x10000 {
				continue
			}
			if s, _, ok := g.clrString(p); ok && len(s) >= 2 {
				nameAt[off]++
				withName = append(withName, named{h, off, s})
			}
		}
	}
	offs := make([]int, 0, len(nameAt))
	for o := range nameAt {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(i, j int) bool { return nameAt[offs[i]] > nameAt[offs[j]] })
	fmt.Printf("  mono NamePtrOff %#x reads a CLR string on %d blocks\n", layout.NamePtrOff, nameAt[layout.NamePtrOff])
	for i, o := range offs {
		if i == 12 {
			break
		}
		fmt.Printf("  name-shaped string pointer at statLife%+#x: %d blocks\n", o, nameAt[o])
	}
	shown := 0
	for _, n := range withName {
		if shown == 25 {
			break
		}
		fmt.Printf("    life@%08x %v  %+#x -> %q\n", n.h.life, n.h.fields, n.off, n.name)
		shown++
	}

	clrPlayer(g, data)

	fmt.Println("\n== Item[] (59 slots) pointer near statLife ==")
	invAt := map[int]int{}
	for _, h := range hits {
		for off := -0x1000; off <= 0x400; off += 4 {
			p, ok := g.u32(uint32(int64(h.life) + int64(off))) //nolint:gosec // a delta from statLife
			if !ok || p < 0x10000 {
				continue
			}
			if n, ok := g.u32(p + 4); ok && n == layout.InventorySlots {
				invAt[off]++
			}
		}
	}
	for o, c := range invAt {
		fmt.Printf("  ptr->array(len 59 at +4) at statLife%+#x: %d blocks (mono InventoryPtrOff %#x)\n", o, c, layout.InventoryPtrOff)
	}

	fmt.Println("\n== code-patch anchors (exec regions) ==")
	an := anchors("internal/patch/anchors.go")
	names := make([]string, 0, len(an))
	for k := range an {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Printf("  %-22s %d matches\n", k, matchCount(g, exec, an[k]))
	}
}

// validBlock is locate.ValidBlock: the same rule the Linux build applies.
var validBlock = locate.ValidBlock
