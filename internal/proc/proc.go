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
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
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

// Mem is read and write access to one process, by pid.
type Mem struct{ PID int }

// New is access to the process with this pid. It opens nothing until it is
// read from.
func New(pid int) *Mem { return &Mem{PID: pid} }

// Regions is the process's writable, scannable memory.
func (m *Mem) Regions() []Region { return m.listing(ParseRegions) }

/*
ExecRegions is the process's executable memory, which is where JIT'd code is.

A pattern search for a method's compiled shape looks here and nowhere else: the
writable regions are the managed heap, and the heap does not hold instructions.
*/
func (m *Mem) ExecRegions() []Region { return m.listing(ParseExecRegions) }

// AllRegions is every mapping this process has.
func (m *Mem) AllRegions() []Region { return m.listing(ParseAllRegions) }

// listing parses this process's maps with one of the readers above.
func (m *Mem) listing(parse func(io.Reader) []Region) []Region {
	f, err := os.Open(m.maps())
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return parse(f)
}

// Read is size bytes at addr, or nothing. A short or failed read is ordinary:
// a region can be unmapped between being listed and being read.
func (m *Mem) Read(addr uint32, size int) []byte {
	f, err := os.Open(m.mem())
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, size)
	n, err := f.ReadAt(buf, int64(addr))
	if n == 0 && err != nil {
		return nil
	}
	return buf[:n]
}

// Write puts data at addr and reports whether all of it landed.
func (m *Mem) Write(addr uint32, data []byte) bool {
	f, err := os.OpenFile(m.mem(), os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	n, err := f.WriteAt(data, int64(addr))
	return err == nil && n == len(data)
}

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

// ExePath is where the mapped game came from, or "" when this process has not
// got it mapped.
func (m *Mem) ExePath() string {
	f, err := os.Open(m.maps())
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimRight(scan.Text(), " \t")
		if !strings.HasSuffix(line, GameExe) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 5 {
			return parts[5]
		}
	}
	return ""
}

func (m *Mem) maps() string { return filepath.Join("/proc", strconv.Itoa(m.PID), "maps") }
func (m *Mem) mem() string  { return filepath.Join("/proc", strconv.Itoa(m.PID), "mem") }

// ErrNoGame is returned when the game is not running.
var ErrNoGame = errors.New("no running " + GameExe + " found. Is Terraria launched " +
	"(Windows build under Proton)?")

/*
FindPID is the pid of the running game.

The game is the one process that maps Terraria.exe *executable*. Proton's
wrapper scripts name the same path on their command lines and never map it that
way, so this does not mistake one of them for the game.
*/
func FindPID() (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("read /proc: %w", err)
	}
	var found []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a process
		}
		if mapsTheGame(filepath.Join("/proc", entry.Name(), "maps")) {
			found = append(found, pid)
		}
	}
	switch len(found) {
	case 0:
		return 0, ErrNoGame
	case 1:
		return found[0], nil
	}
	// Extremely unlikely, and said plainly rather than picked blindly.
	return 0, fmt.Errorf("multiple %s processes found: %v", GameExe, found)
}

// mapsTheGame reports whether a maps listing has the game mapped executable.
func mapsTheGame(path string) bool {
	f, err := os.Open(path) //nolint:gosec // a path built from a /proc entry
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if strings.Contains(line, GameExe) && strings.Contains(line, "r-xp") &&
			strings.HasSuffix(strings.TrimRight(line, " \t"), GameExe) {
			return true
		}
	}
	return false
}
