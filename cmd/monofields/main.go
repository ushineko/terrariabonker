/*
Command monofields asks the mono runtime for field offsets by name, instead of
inferring them.

Every offset in this project used to be *derived* -- from declaration order,
from a value signature in SetDefaults, from watching a number change in game.
That works until it quietly does not: Projectile.active was read at 0x03C for
eight releases, which is really Entity.wet, and nothing caught it because a
fishing bobber floats in water and is therefore always wet. Every test agreed
with the wrong number.

The runtime knows the answer. MonoClassField on 32-bit is

	struct MonoClassField { MonoType *type; const char *name; MonoClass *parent; int offset; }

so finding a field-name string and then a pointer to it puts the offset eight
bytes further on. Deliberately, nothing here walks MonoVTable or MonoClass:
those layouts shift between mono builds, and avoiding them is what makes this
survive an update.

	sudo monofields --verify              # check this project's constants
	sudo monofields active tileCollide    # what does the runtime say?
	sudo monofields --dump 0x02852ee8     # every field of one class

Read-only. It never writes to the game.
*/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
expected is what this project believes, grouped by the class that *declares*
each field.

An inherited field lives on the base class and does not appear in the
subclass's own table, so `position` is checked against Entity rather than
Projectile. Keep this in step with internal/layout.
*/
var expected = map[string]map[string]uint32{
	"Entity": {
		"whoAmI": 0x008, "position": 0x00C, "velocity": 0x014, "oldPosition": 0x01C,
		"direction": 0x030, "width": 0x034, "height": 0x038, "wet": 0x03C,
	},
	"Projectile": {
		"ai": 0x044, "localAI": 0x048, "active": 0x078, "bobber": 0x088,
		"scale": 0x08C, "type": 0x094, "alpha": 0x098, "aiStyle": 0x0B0,
		"timeLeft": 0x0B4, "damage": 0x0BC, "hostile": 0x0C8, "knockBack": 0x0CC,
		"friendly": 0x0D0, "penetrate": 0x0D4, "maxPenetrate": 0x0DC,
		"tileCollide": 0x100, "extraUpdates": 0x104,
	},
	"Item": {
		"useAnimation": 0x080, "useTime": 0x084, "pick": 0x090, "damage": 0x0AC,
		"knockBack": 0x0B0, "healLife": 0x0B4, "healMana": 0x0B8, "scale": 0x0CC,
		"shootSpeed": 0x100, "mana": 0x11C, "crit": 0x150, "prefix": 0x15C,
	},
	"Player": {"statLife": 0x738, "itemAnimation": 0x0BCC},
}

/*
notWritten is what this project knows about and deliberately does not write.

Verified all the same, so that "unverified" never quietly becomes "forgotten".
*/
var notWritten = map[string]map[string]uint32{
	"Item": {"armorPenetration": 0x154, "bonusTagDamage": 0x158, "reuseDelay": 0x164},
}

// maxSaneOffset bounds a plausible field: anything past this is a false
// positive rather than a field.
const maxSaneOffset = 0x4000

// field is one MonoClassField the scan turned up.
type field struct {
	class  uint32
	name   string
	offset uint32
}

func main() {
	verify := flag.Bool("verify", false, "check this project's offsets against the runtime")
	dump := flag.String("dump", "", "dump every field of a MonoClass")
	flag.Parse()

	pid, err := proc.FindPID()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mem := proc.New(pid)
	regions := readableRegions(pid)
	if len(regions) == 0 {
		fmt.Fprintln(os.Stderr, "no readable regions -- run this under sudo")
		os.Exit(1)
	}

	switch {
	case *verify:
		os.Exit(runVerify(mem, regions))
	case *dump != "":
		class, err := strconv.ParseUint(strings.TrimPrefix(*dump, "0x"), 16, 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%q is not a class address\n", *dump)
			os.Exit(2)
		}
		rows := dumpClass(mem, regions, uint32(class)) //nolint:gosec // parsed as 32 bits
		fmt.Printf("class %#010x: %d named fields\n\n", class, len(rows))
		for _, r := range rows {
			fmt.Printf("  %#06x  %6d  %s\n", r.offset, r.offset, r.name)
		}
	case flag.NArg() > 0:
		os.Exit(lookUp(mem, regions, flag.Args()))
	default:
		fmt.Fprintln(os.Stderr, "give some field names, or --verify / --dump")
		os.Exit(2)
	}
}

/*
readableRegions is every readable non-device mapping.

Wider than what the trainer scans: that is writable-only, which is right for
finding game objects and wrong here, because the field-name strings live in the
read-only mapped assembly image.
*/
func readableRegions(pid int) []proc.Region {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return nil
	}
	var out []proc.Region
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.Contains(fields[1], "r") {
			continue
		}
		if len(fields) > 5 {
			switch path := fields[5]; {
			case strings.HasPrefix(path, "/dev/"), path == "[vvar]", path == "[vsyscall]":
				continue
			}
		}
		bounds := strings.SplitN(fields[0], "-", 2)
		start, err1 := strconv.ParseUint(bounds[0], 16, 64)
		end, err2 := strconv.ParseUint(bounds[1], 16, 64)
		if err1 != nil || err2 != nil || end > 1<<32 {
			continue
		}
		out = append(out, proc.Region{
			Start: uint32(start), End: uint32(end), Readable: true, //nolint:gosec // bounded above
		})
	}
	return out
}

// findStrings is address to name, for every place one of these names appears
// NUL-terminated.
func findStrings(mem *proc.Mem, regions []proc.Region, names []string) map[uint32]string {
	found := map[uint32]string{}
	for _, r := range regions {
		buf := mem.Read(r.Start, r.Size())
		if len(buf) == 0 {
			continue
		}
		for _, name := range names {
			want := append([]byte(name), 0)
			for i := 0; ; {
				at := indexFrom(buf, want, i)
				if at < 0 {
					break
				}
				found[r.Start+uint32(at)] = name //nolint:gosec // an offset in a 32-bit region
				i = at + 1
			}
		}
	}
	return found
}

func indexFrom(haystack, needle []byte, from int) int {
	if from >= len(haystack) {
		return -1
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

/*
findRecords is every MonoClassField naming one of these strings.

A pointer to the name string *is* the record's name slot, so the record starts
one word earlier and the offset sits two words after the pointer.
*/
func findRecords(mem *proc.Mem, regions []proc.Region, strs map[uint32]string) []field {
	if len(strs) == 0 {
		return nil
	}
	var out []field
	for _, r := range regions {
		buf := mem.Read(r.Start, r.Size())
		for i := 0; i+4 <= len(buf); i += 4 {
			ptr := binary.LittleEndian.Uint32(buf[i:])
			name, isName := strs[ptr]
			if !isName || i < 4 || i+12 > len(buf) {
				continue
			}
			class := binary.LittleEndian.Uint32(buf[i+4:])
			offset := binary.LittleEndian.Uint32(buf[i+8:])
			// The offset counts from the object start and already includes the
			// mono header.
			if class > 0 && class < 0xFFFFFFFF && offset <= maxSaneOffset {
				out = append(out, field{class: class, name: name, offset: offset})
			}
		}
	}
	return out
}

/*
dumpClass is every named field of a class, in offset order.

Static fields share the table and their offsets index static storage instead, so
a cluster of them at low offsets is expected rather than a contradiction.
*/
func dumpClass(mem *proc.Mem, regions []proc.Region, class uint32) []field {
	var rows []field
	seen := map[string]bool{}
	for _, r := range regions {
		buf := mem.Read(r.Start, r.Size())
		for i := 0; i+4 <= len(buf); i += 4 {
			if binary.LittleEndian.Uint32(buf[i:]) != class || i < 8 {
				continue
			}
			// The parent is the third word, so the record starts two back.
			rec := i - 8
			if rec+16 > len(buf) {
				continue
			}
			offset := binary.LittleEndian.Uint32(buf[rec+12:])
			if offset > maxSaneOffset {
				continue
			}
			name := cstr(mem, binary.LittleEndian.Uint32(buf[rec+4:]))
			if name != "" && !seen[name] {
				seen[name] = true
				rows = append(rows, field{class: class, name: name, offset: offset})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].offset < rows[j].offset })
	return rows
}

// cstr is a NUL-terminated printable ASCII string, or nothing.
func cstr(mem *proc.Mem, addr uint32) string {
	raw := mem.Read(addr, 64)
	if len(raw) == 0 {
		return ""
	}
	if end := indexFrom(raw, []byte{0}, 0); end >= 0 {
		raw = raw[:end]
	}
	if len(raw) == 0 {
		return ""
	}
	for _, c := range raw {
		if c < 32 || c >= 127 {
			return ""
		}
	}
	return string(raw)
}

// resolveClasses picks each class's address: the one declaring the most of the
// fields expected of it.
func resolveClasses(records []field, want map[string]map[string]uint32) map[string]uint32 {
	out := map[string]uint32{}
	for class, fields := range want {
		score := map[uint32]int{}
		for _, r := range records {
			if off, named := fields[r.name]; named && off == r.offset {
				score[r.class]++
			}
		}
		best, bestScore := uint32(0), 0
		for addr, n := range score {
			if n > bestScore || (n == bestScore && addr < best) {
				best, bestScore = addr, n
			}
		}
		if bestScore > 0 {
			out[class] = best
		}
	}
	return out
}

// runVerify checks every constant this project believes against the runtime.
func runVerify(mem *proc.Mem, regions []proc.Region) int {
	groups := map[string]map[string]uint32{}
	for class, fields := range expected {
		groups[class] = map[string]uint32{}
		for name, off := range fields {
			groups[class][name] = off
		}
	}
	for class, fields := range notWritten {
		if groups[class] == nil {
			groups[class] = map[string]uint32{}
		}
		for name, off := range fields {
			groups[class][name] = off
		}
	}

	var names []string
	for _, fields := range groups {
		for name := range fields {
			names = append(names, name)
		}
	}
	records := findRecords(mem, regions, findStrings(mem, regions, names))
	classes := resolveClasses(records, groups)

	bad := 0
	for _, class := range sortedKeys(groups) {
		fields := groups[class]
		addr, found := classes[class]
		if !found {
			fmt.Printf("%s: NOT FOUND -- no class declares these fields\n", class)
			bad += len(fields)
			continue
		}
		actual := map[string]uint32{}
		for _, r := range records {
			if r.class == addr {
				actual[r.name] = r.offset
			}
		}
		fmt.Printf("\n%s  (MonoClass %#010x)\n", class, addr)
		for _, name := range byOffset(fields) {
			want := fields[name]
			note := ""
			if _, unwritten := notWritten[class][name]; unwritten {
				note = "  [not written]"
			}
			switch got, declared := actual[name]; {
			case declared && got == want:
				fmt.Printf("  ok       %-22s %#06x%s\n", name, want, note)
			case !declared:
				fmt.Printf("  MISSING  %-22s %#06x not declared here%s\n", name, want, note)
				bad++
			default:
				fmt.Printf("  WRONG    %-22s we say %#06x, runtime says %#06x\n", name, want, got)
				bad++
			}
		}
	}
	if bad > 0 {
		fmt.Printf("\n%d mismatch(es)\n", bad)
		return 1
	}
	fmt.Println("\nall offsets agree with the runtime")
	return 0
}

// lookUp reports what the runtime says about some field names.
func lookUp(mem *proc.Mem, regions []proc.Region, names []string) int {
	records := findRecords(mem, regions, findStrings(mem, regions, names))
	if len(records) == 0 {
		fmt.Println("no field records found -- is the game running with a world loaded?")
		return 1
	}
	for _, name := range names {
		seen := map[[2]uint32]bool{}
		var hits [][2]uint32
		for _, r := range records {
			if r.name != name {
				continue
			}
			key := [2]uint32{r.class, r.offset}
			if !seen[key] {
				seen[key] = true
				hits = append(hits, key)
			}
		}
		sort.Slice(hits, func(i, j int) bool {
			if hits[i][0] != hits[j][0] {
				return hits[i][0] < hits[j][0]
			}
			return hits[i][1] < hits[j][1]
		})
		fmt.Printf("\n%s:\n", name)
		for _, h := range hits {
			fmt.Printf("  MonoClass %#010x  offset %#06x (%d)\n", h[0], h[1], h[1])
		}
	}
	return 0
}

func sortedKeys(m map[string]map[string]uint32) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// byOffset is a class's field names in declaration order, which is how they
// read against a layout table.
func byOffset(fields map[string]uint32) []string {
	out := make([]string, 0, len(fields))
	for name := range fields {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return fields[out[i]] < fields[out[j]] })
	return out
}
