package proc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysMem is nothing on Linux: every access opens /proc afresh.
type sysMem struct{}

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

/*
MappedInode is the inode a path is mapped from, and whether it is mapped
executable at all.

The mapping carries the inode it was made from, so comparing it with the file on
disk says whether the process is running the code that path now describes.
Replacing a file leaves the old mapping in place, and reading the new file would
then report a build that is not running.
*/
func MappedInode(pid int, path string) (uint64, bool) {
	f, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "maps"))
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		parts := strings.Fields(scan.Text())
		if len(parts) < 6 || parts[5] != path || !strings.Contains(parts[1], "x") {
			continue
		}
		inode, err := strconv.ParseUint(parts[4], 10, 64)
		if err != nil {
			return 0, false
		}
		return inode, true
	}
	return 0, false
}

// Alive reports whether a pid is still there, which is how a game restart is
// noticed.
func Alive(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}
