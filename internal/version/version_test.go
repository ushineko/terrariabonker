package version_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The build gate is compared with the Python's, case for case.

What it decides is whether anything is allowed to write to the game, so its two
failure directions are not symmetric. Refusing a build that would have worked
costs the player their cheats; accepting one whose offsets have moved writes
numbers into the wrong fields of a live save. And saying "I cannot tell" is not a
third kind of wrong -- it is recoverable, because the caller retries.
*/

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

// The build this project targets, and the key it is known by, are the same.
func TestTheKnownBuildMatchesThePython(t *testing.T) {
	var want map[string]string
	askPython(t, `
import json
from terrariabonker import version as V
print(json.dumps({"version": V.KNOWN_VERSION, "buildid": V.KNOWN_BUILDID,
                  "key": V.KNOWN_BUILD_KEY}))`, &want)

	require.Equal(t, want["version"], version.KnownVersion)
	require.Equal(t, want["buildid"], version.KnownBuildID)
	require.Equal(t, want["key"], version.KnownBuildKey)
}

// A build key reads the same, including when half of it is missing.
func TestBuildKeyMatchesThePython(t *testing.T) {
	cases := [][2]string{
		{"1.4.5.8", "24893155"},
		{"1.4.5.7", "24825745"},
		{"", "24893155"},
		{"1.4.5.8", ""},
		{"", ""},
	}
	var want []string
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import version as V
print(json.dumps([V.build_key(v or None, b or None) for v, b in %s]))`,
		pyPairs(cases)), &want)

	for i, c := range cases {
		require.Equalf(t, want[i], version.BuildKey(c[0], c[1]),
			"the key for %q/%q differs", c[0], c[1])
	}
}

/*
Every build is classified the same way, and for the same stated reason.

The reason is compared as well as the verdict: it is what the window puts in
front of somebody deciding whether to override the gate, and a verdict without
its reason is a number nobody can act on.
*/
func TestCompatibilityMatchesThePython(t *testing.T) {
	cases := [][2]string{
		{version.KnownVersion, version.KnownBuildID}, // the build this targets
		{version.KnownVersion, "99999999"},           // a Steam rebuild of it
		{version.KnownVersion, ""},                   // no manifest to read
		{"1.4.5.9", version.KnownBuildID},            // a fourth-component hotfix
		{"1.4.5", version.KnownBuildID},              // the same three, spelled shorter
		{"1.4.6.0", version.KnownBuildID},            // a real update
		{"1.3.5.3", ""},                              // the previous era
		{"2.0.50727", ""},                            // the runtime's own version
		{"", ""},                                     // nothing readable yet
	}

	var want [][]string
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import version as V
print(json.dumps([list(V.compatibility(v or None, b or None)) for v, b in %s]))`,
		pyPairs(cases)), &want)

	for i, c := range cases {
		level, msg := version.Compatibility(c[0], c[1])
		require.Equalf(t, want[i][0], string(level), "%q/%q is classified differently", c[0], c[1])
		require.Equalf(t, want[i][1], msg, "%q/%q is explained differently", c[0], c[1])
	}
}

/*
A version is read out of memory the same way, including the cases that once got
it wrong.

The frequency vote is the fallback, and it is the part with history: the runtime
string outnumbering the game's at startup, and a single baked-in constant being
mistaken for the version being run.
*/
func TestDetectingFromMemoryMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name    string
		planted map[string]int // version string -> how many copies
	}{
		{"nothing at all", nil},
		{"one copy, which is a constant and not the running version",
			map[string]int{"1.4.5.8": 1}},
		{"two copies, which is the running version",
			map[string]int{"1.4.5.8": 2}},
		// What actually happens on this build: stale data outnumbers the truth.
		{"stale data outnumbering the real one",
			map[string]int{"1.4.5.7": 4, "1.4.5.8": 2}},
		// And the one that concluded the game was "Terraria 2.0.50727".
		{"the runtime's own version, which is not a game version",
			map[string]int{"2.0.50727": 6}},
		{"the runtime alongside the game",
			map[string]int{"2.0.50727": 6, "1.4.5.8": 2}},
		{"a short version, which is not one", map[string]int{"1.4": 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want any
			askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import version as V
mem = FakeMem(0x10000000, 0x8000)
mem.exe_path = lambda: None
at = 0x10000100
for text, n in %s:
    for _ in range(n):
        mem.poke_bytes(at, ("v" + text).encode("utf-16-le"))
        at += 0x80
print(json.dumps(V.detect_version(mem)))`, pyPlant(c.planted)), &want)

			mem := plant(c.planted)
			got := version.Detect(mem, -1)
			if want == nil {
				require.Empty(t, got, "a version was read where the Python read none")
				return
			}
			require.Equal(t, want, got, "a different version was read")
		})
	}
}

// noExe is a fake with no executable behind it, so only the memory scan runs.
type noExe struct{ *memtest.FakeMem }

func (noExe) ExePath() string { return "" }

// plant writes each version string into memory the given number of times, as the
// game stores it.
func plant(counts map[string]int) noExe {
	mem := memtest.New(0x10000000, 0x8000)
	at := uint32(0x10000100)
	for _, text := range sortedKeys(counts) {
		for range counts[text] {
			mem.PokeBytes(at, utf16le("v"+text))
			at += 0x80
		}
	}
	return noExe{mem}
}

// utf16le is a string as the game stores it.
func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

/*
The build id comes from Steam's manifest, three directories up from the
executable.

A missing manifest is nothing rather than an error: the game can be run from
outside Steam, and a build key of "1.4.5.8+?" is still a usable one.
*/
func TestReadingTheBuildIDMatchesThePython(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "steamapps", "common", "Terraria")
	require.NoError(t, os.MkdirAll(game, 0o755))
	exe := filepath.Join(game, "Terraria.exe")
	require.NoError(t, os.WriteFile(exe, []byte("MZ"), 0o644))

	manifest := filepath.Join(dir, "steamapps", "appmanifest_105600.acf")
	require.NoError(t, os.WriteFile(manifest, []byte(
		"\"AppState\"\n{\n\t\"buildid\"\t\t\"24893155\"\n}\n"), 0o644))

	for _, c := range []struct{ name, path string }{
		{"beside a manifest", exe},
		{"no path at all", ""},
		{"a path with no manifest", filepath.Join(dir, "elsewhere", "x", "y", "Terraria.exe")},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want any
			askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import version as V
print(json.dumps(V.read_buildid(%q or None)))`, c.path), &want)

			got := version.ReadBuildID(c.path)
			if want == nil {
				require.Empty(t, got, "a build id was read where the Python read none")
				return
			}
			require.Equal(t, want, got, "a different build id")
		})
	}
}

/*
The version read out of the game's own executable is the authority.

Both implementations prefer it to the memory scan, and both accept it only when
the file holds exactly one plausible version: a file with two is a file this
cannot speak for.
*/
func TestReadingTheVersionFromAnExecutableMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name     string
		contents []string
	}{
		{"one version", []string{"1.4.5.8"}},
		{"the same one twice", []string{"1.4.5.8", "1.4.5.8"}},
		{"two different ones", []string{"1.4.5.8", "1.4.5.7"}},
		{"none", nil},
		{"one, beside the runtime's", []string{"1.4.5.8", "2.0.50727"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Terraria.exe")
			var body []byte
			for _, v := range c.contents {
				body = append(body, utf16le("v"+v)...)
				body = append(body, make([]byte, 16)...)
			}
			require.NoError(t, os.WriteFile(path, body, 0o644))

			var want any
			askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import version as V
print(json.dumps(V._version_from_exe(%q)))`, path), &want)

			got := version.VersionFromExe(path)
			if want == nil {
				require.Empty(t, got, "a version was read where the Python read none")
				return
			}
			require.Equal(t, want, got, "a different version was read from the file")
		})
	}
}
