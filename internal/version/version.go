/*
Package version works out which build of the game is running, and whether this
program's offsets were derived against it.

It matters because every number this project knows -- field offsets, byte
patterns -- was derived by hand against one build, and a game update moves them.
Being wrong in the reassuring direction is the dangerous one: a confident wrong
answer aborts everything with a message about the offsets, while "I cannot read
the version yet" is recoverable, because the caller retries.

Ported from terrariabonker/version.py (spec 051, step 5).
*/
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/ushineko/terrariabonker/internal/proc"
)

// The build this project currently targets, and the Steam build id that
// fingerprints it.
const (
	KnownVersion = "1.4.5.8"
	KnownBuildID = "24893155"
)

/*
BuildKey is the identity of one exact build.

The version string alone is not enough: Steam rebuilds keep the version and
change the build id, and a derived byte pattern is really pinned to the rebuild.
It is the key of the anchor-verification ledger.
*/
func BuildKey(version, buildid string) string {
	return unknownIfEmpty(version) + "+" + unknownIfEmpty(buildid)
}

// unknownIfEmpty is a missing half of a build key, spelled so the key is still
// readable.
func unknownIfEmpty(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// KnownBuildKey is the build key of the build this targets.
var KnownBuildKey = BuildKey(KnownVersion, KnownBuildID)

// verRe matches "vX.Y.Z" or "vX.Y.Z.W" as UTF-16, which is how the version
// string appears both in the assembly and in memory.
var verRe = regexp.MustCompile(`v\x00((?:[0-9]\x00)+(?:\.\x00(?:[0-9]\x00)+)+)`)

/*
maxComponent is larger than any part of a Terraria version and smaller than the
runtime's own.

The mono runtime's version string, the .NET 2.0 build number, matches the same
shape -- and right after the game launches it can be the *only* match in memory,
because Terraria's own string has not been allocated yet. Left unfiltered it won
the vote and the program concluded the game was "Terraria 2.0.50727".
*/
const maxComponent = 1000

/*
minOccurrences is how many copies of a version string make it the running one.

One occurrence is a constant baked into the executable, not the version being
run. Measured across a real launch: for the first twenty seconds or so the only
candidates are the real version and the runtime's, one occurrence each -- a tie
broken by scan order, which is how a startup misread could report either with
confidence. The live version shows up two to four times, and only once the game
has reached its menu.
*/
const minOccurrences = 2

/*
Mem is what the detector reads: the process's own memory and what it maps.

The pid is passed separately rather than asked of this, because the two things
that need it read /proc directly and a caller that has only a buffer to offer --
a test -- can still supply the rest.
*/
type Mem interface {
	ExePath() string
	Regions() []proc.Region
	Read(addr uint32, size int) []byte
}

// exeCache remembers a version read from a file, keyed by what the file was when
// it was read.
var exeCache sync.Map // path -> exeEntry

// exeEntry is one cached reading and the file it came from.
type exeEntry struct {
	stamp   string
	version string
}

/*
mappedExe is the executable the process maps, and whether that mapping still
refers to the file on disk.

Replacing a file leaves an existing mapping on the old inode, so a process can be
executing code the path no longer describes. Reading the file then reports a
build that is not running, which the inode check is a cheap guard against.

That is a precaution rather than an observed failure. The executable's timestamp
did once move during a Steam validation pass while a session was in progress, but
the mixed build key recorded that day does not need this to explain it: the
version came from the frequency vote, which returns a stale one, while the build
id came from Steam's manifest. Reading the executable is what fixed that; this
only stops the executable being trusted when it might not be the one running.
*/
func mappedExe(mem Mem, pid int) (string, bool) {
	path := mem.ExePath()
	if path == "" {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	inode, ok := inodeOf(info)
	if !ok {
		return path, false
	}
	mapped, ok := proc.MappedInode(pid, path)
	return path, ok && mapped == inode
}

/*
FromExe is the version literal compiled into the executable, when there is
exactly one.

This is the authority. The game's version is a string constant in its own
assembly; everything else in the process that looks like a version belongs to
somebody else -- a runtime path, or stale data left by a previous build, which is
what a frequency vote kept choosing.
*/
func FromExe(path string) string {
	stamp, err := stampOf(path)
	if err != nil {
		return ""
	}
	if cached, ok := exeCache.Load(path); ok {
		if entry := cached.(exeEntry); entry.stamp == stamp {
			return entry.version
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // the game's own executable, as mapped
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	for _, m := range verRe.FindAllSubmatch(data, -1) {
		if v := readVersion(m[1]); v != "" {
			found[v] = true
		}
	}
	var got string
	if len(found) == 1 {
		for v := range found {
			got = v
		}
	}
	exeCache.Store(path, exeEntry{stamp: stamp, version: got})
	return got
}

// readVersion is a matched UTF-16 version as text, or nothing when it is not one
// this could be.
func readVersion(raw []byte) string {
	v := strings.ReplaceAll(string(raw), "\x00", "")
	if strings.Count(v, ".") < 2 { // X.Y.Z or X.Y.Z.W, not "v1.0"
		return ""
	}
	if !plausible(v) {
		return ""
	}
	return v
}

// plausible reports whether a string could be a game version at all.
func plausible(v string) bool {
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n >= maxComponent {
			return false
		}
	}
	return true
}

/*
Detect is the version of the build this process is running, or nothing when it
cannot be told.

Read from the executable the process maps, after checking the mapping still
refers to the file on disk. Scanning live memory was tried first and is kept only
as a fallback: the process holds several version-shaped strings that are not the
game's, a frequency vote picks whichever happens to be most numerous, and the
game's own literal is not it. On this build the heap holds four copies of a stale
version in leftover data against a single copy of the real one.
*/
func Detect(mem Mem, pid int) string {
	if path, current := mappedExe(mem, pid); current {
		if v := FromExe(path); v != "" {
			return v
		}
	}
	counts := map[string]int{}
	for _, r := range mem.Regions() {
		buf := mem.Read(r.Start, r.Size())
		if len(buf) == 0 {
			continue
		}
		for _, m := range verRe.FindAllSubmatch(buf, -1) {
			if v := readVersion(m[1]); v != "" {
				counts[v]++
			}
		}
	}
	best, most := "", 0
	for v, n := range counts {
		if n < minOccurrences {
			continue
		}
		// Ties go to the lower version, so the answer does not depend on the
		// order a map was walked in.
		if n > most || (n == most && v < best) {
			best, most = v, n
		}
	}
	return best
}

// runtimeRe finds the runtime's version in the paths the process maps.
var runtimeRe = regexp.MustCompile(`wine-mono-([0-9][0-9.]*[0-9])`)

/*
DetectRuntime is the .NET runtime executing the game.

Worth knowing because the code patches match machine code that runtime's
compiler *emitted*, not anything in the game's own executable. The same game
under a different runtime compiles to different bytes, so a Proton update can
break a cheat with the game untouched -- and without this, that would look like
the game had changed.

Read from the module paths the process maps, which carry the version. The files
themselves live inside Proton's container and cannot be read from outside it.
*/
func DetectRuntime(pid int) string {
	maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	for _, m := range runtimeRe.FindAllSubmatch(maps, -1) {
		found[string(m[1])] = true
	}
	if len(found) == 0 {
		return ""
	}
	lowest := ""
	for v := range found {
		if lowest == "" || v < lowest {
			lowest = v
		}
	}
	return "wine-mono-" + lowest
}

// buildIDRe pulls the build id out of Steam's manifest.
var buildIDRe = regexp.MustCompile(`"buildid"\s+"(\d+)"`)

// ReadBuildID is the Steam build id from the manifest beside the game.
func ReadBuildID(exePath string) string {
	if exePath == "" {
		return ""
	}
	// .../steamapps/common/Terraria/Terraria.exe -> .../steamapps/appmanifest_105600.acf
	dir := exePath
	for range 3 {
		dir = filepath.Dir(dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "appmanifest_105600.acf")) //nolint:gosec // Steam's own manifest
	if err != nil {
		return ""
	}
	if m := buildIDRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// Level is how far a running build is from the one this was derived against.
type Level string

// The four answers, from best to worst. Unknown is not the worst: it means the
// version could not be read, which is recoverable, where Incompatible is a
// statement about a build that was read.
const (
	Exact        Level = "exact"
	Hotfix       Level = "hotfix"
	Incompatible Level = "incompatible"
	Unknown      Level = "unknown"
)

// Compatibility classifies a build against the known-good one, and says why.
func Compatibility(foundVersion, foundBuildID string) (Level, string) {
	switch {
	case foundVersion == "":
		return Unknown, "could not read the game version from memory"

	case foundVersion == KnownVersion:
		if foundBuildID != "" && foundBuildID != KnownBuildID {
			return Hotfix, fmt.Sprintf("version %s matches but Steam buildid %s != known "+
				"%s; likely a rebuild, offsets probably fine but unverified",
				foundVersion, foundBuildID, KnownBuildID)
		}
		return Exact, fmt.Sprintf("Terraria %s (matches known-good build)", foundVersion)

	case triple(foundVersion) == triple(KnownVersion):
		return Hotfix, fmt.Sprintf("hotfix %s vs known %s: offsets MIGHT still be valid "+
			"but are unproven on this build", foundVersion, KnownVersion)
	}
	return Incompatible, fmt.Sprintf("Terraria %s differs from %s in major/minor/patch; "+
		"the offsets are almost certainly wrong", foundVersion, KnownVersion)
}

// triple is the first three components of a version, which is the granularity
// the offsets are pinned at: a fourth-component hotfix has never moved them.
func triple(v string) [3]int {
	var out [3]int
	for i, part := range strings.Split(v, ".") {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{-1, -1, -1}
		}
		out[i] = n //nolint:gosec // the loop breaks at i >= 3
	}
	return out
}
