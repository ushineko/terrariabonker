/*
Package proc is access to one running process's memory.

Terraria runs as the 32-bit Windows build under Proton, and on a box with
ptrace_scope set, reading another process's memory needs root. Nothing here
elevates: a caller that needs privilege has it or fails, and the window never
calls this at all -- it reaches memory by shelling out to the CLI under sudo,
which is the architecture rule in AGENTS.md and does not change because the CLI
is being rewritten.

Nothing here knows about Terraria's data structures either. This module finds
the game, lists what parts of it are readable, and moves bytes. What those bytes
mean is locate's business.

The Go half of terrariabonker/proc.py (spec 051, step 3).
*/
package proc

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"
	"strconv"
	"strings"
)

// GameExe is the mapped file that identifies the game among Proton's processes.
const GameExe = "Terraria.exe"

// Region is a half-open span of a process's address space.
type Region struct {
	Start, End uint32
	// Writable says the CPU may write here as well as read it. It matters only
	// for executable regions: a code cave is borrowed padding inside somebody
	// else's mapping, and almost none of those are writable.
	Writable bool
	// Executable says the CPU may run what is here. An arena this program had
	// the game allocate for it is the rare mapping that is both.
	Executable bool
	// Readable says the mapping can be read at all. A few cannot be, and asking
	// one of those what kind of image it holds reads nothing.
	Readable bool
}

// Size is how many bytes the region covers.
func (r Region) Size() int { return int(r.End - r.Start) }

/*
ParseRegions is the writable, scannable regions of a /proc/<pid>/maps listing.

The managed heap where Terraria's objects live is anonymous writable memory.
Device mappings are skipped because they are not scannable RAM: reading one can
stall or fail, and a scan that walks into a graphics card's aperture is a scan
that hangs rather than one that finds nothing.

Parsing is separate from opening the file so it can be tested against a real
listing without a process to point it at.
*/
func ParseRegions(r io.Reader) []Region { return parseListing(r, "w", false) }

/*
ParseExecRegions is the executable regions of the same listing.

Separate from the writable ones because they are looked at for different
reasons: the writable regions are the managed heap, where a player lives, and
the executable ones are JIT'd code, where a method's compiled shape can be
matched.
*/
func ParseExecRegions(r io.Reader) []Region { return parseListing(r, "x", false) }

/*
ParseAllRegions is every mapping, whatever its permissions, device mappings
included.

The two listings above answer "where could this be?"; this one answers "what is
already taken". Finding somewhere to put an arena means looking at the gaps
between *all* the mappings, and a graphics card's aperture occupies its addresses
exactly as firmly as anything else -- the reason the other two skip it is that
reading it can stall, which is not a reason to pretend the space is free.
*/
func ParseAllRegions(r io.Reader) []Region { return parseListing(r, "", true) }

// parseListing keeps the regions whose permissions carry want, and device
// mappings only when they are asked for.
func parseListing(r io.Reader, want string, keepDevices bool) []Region {
	var out []Region
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		region, ok := parseRegion(scan.Text(), want, keepDevices)
		if ok {
			out = append(out, region)
		}
	}
	return out
}

// parseRegion reads one line of a maps listing, and reports whether it is a
// region worth scanning.
func parseRegion(line, want string, keepDevices bool) (Region, bool) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return Region{}, false
	}
	if !strings.Contains(parts[1], want) {
		return Region{}, false
	}
	writable := strings.Contains(parts[1], "w")
	executable := strings.Contains(parts[1], "x")
	readable := strings.HasPrefix(parts[1], "r")
	if !keepDevices && len(parts) > 5 && strings.HasPrefix(parts[5], "/dev/") {
		return Region{}, false
	}
	lo, hi, ok := strings.Cut(parts[0], "-")
	if !ok {
		return Region{}, false
	}
	start, err := strconv.ParseUint(lo, 16, 64)
	if err != nil {
		return Region{}, false
	}
	end, err := strconv.ParseUint(hi, 16, 64)
	if err != nil {
		return Region{}, false
	}
	// An address that will not fit in a uint32 is skipped rather than
	// truncated. Not because a 64-bit address cannot be read -- it obviously
	// can -- but because every address in this game is 32 bits: it runs as the
	// 32-bit Windows build, its pointers are four bytes, and an address type of
	// uint32 makes a truncation bug impossible instead of latent. Measured on
	// the running game: 1,473 regions, 1.59 GiB, not one of them above 4 GB.
	//
	// So this never fires on the game. It fires on a listing that is not the
	// game's, which is what the test feeds it.
	if start > math.MaxUint32 || end > math.MaxUint32 {
		return Region{}, false
	}
	return Region{Start: uint32(start), End: uint32(end),
		Writable: writable, Executable: executable, Readable: readable}, true
}

/*
Mem is read and write access to one process, by pid.

How it reaches the process is the platform's: /proc/<pid>/mem on Linux, a
process handle on Windows. sys holds whatever that needs between calls.
*/
type Mem struct {
	PID int
	sys *sysMem
}

// New is access to the process with this pid. It opens nothing until it is
// read from.
func New(pid int) *Mem { return &Mem{PID: pid, sys: &sysMem{}} }

// ReadU32 is one little-endian word, and whether it was there.
func (m *Mem) ReadU32(addr uint32) (uint32, bool) {
	b := m.Read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

// ReadI32 is the same word read as the signed field most of the game's are.
func (m *Mem) ReadI32(addr uint32) (int32, bool) {
	v, ok := m.ReadU32(addr)
	return int32(v), ok //nolint:gosec // a word read as the signed field it is
}

// WriteI32 puts one signed word at addr.
func (m *Mem) WriteI32(addr uint32, value int32) bool {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(value)) //nolint:gosec // a word, as its bits
	return m.Write(addr, b[:])
}

// ReadF32 is one single-precision float. Knockback, scale and shoot speed are
// floats, and a modifier scales them, so they cannot go through the integer
// path without losing the fraction.
func (m *Mem) ReadF32(addr uint32) (float32, bool) {
	v, ok := m.ReadU32(addr)
	return math.Float32frombits(v), ok
}

// WriteF32 puts one single-precision float at addr.
func (m *Mem) WriteF32(addr uint32, value float32) bool {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(value))
	return m.Write(addr, b[:])
}
