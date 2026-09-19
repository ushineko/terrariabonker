package version_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The build gate, case for case.

What it decides is whether anything is allowed to write to the game, so its two
failure directions are not symmetric. Refusing a build that would have worked
costs the player their cheats; accepting one whose offsets have moved writes
numbers into the wrong fields of a live save. And saying "I cannot tell" is not
a third kind of wrong -- it is recoverable, because the caller retries.

Every expected value here was agreed with the implementation this was ported
from, while both existed. They are written out rather than compared, now that
there is only one.
*/

// The build this project targets, and the key it is known by.
func TestTheKnownBuild(t *testing.T) {
	require.Equal(t, "1.4.5.8", version.KnownVersion)
	require.Equal(t, "24893155", version.KnownBuildID)
	require.Equal(t, "1.4.5.8+24893155", version.KnownBuildKey,
		"the key is the version and the build id, joined")
}

/*
A build key reads the same when half of it is missing.

The half that is missing is written as a question mark rather than left out: a
key of "1.4.5.8" and a key of "1.4.5.8+?" are different claims, and the ledger
of verified builds is keyed on the difference.
*/
func TestBuildKey(t *testing.T) {
	for _, c := range []struct {
		version, buildID, want string
	}{
		{"1.4.5.8", "24893155", "1.4.5.8+24893155"},
		{"1.4.5.7", "24825745", "1.4.5.7+24825745"},
		{"", "24893155", "?+24893155"},
		{"1.4.5.8", "", "1.4.5.8+?"},
		{"", "", "?+?"},
	} {
		require.Equalf(t, c.want, version.BuildKey(c.version, c.buildID),
			"the key for %q/%q", c.version, c.buildID)
	}
}

/*
Every build is classified, and says why.

The reason matters as much as the verdict: it is what the window puts in front
of somebody deciding whether to override the gate, and a verdict without its
reason is a word nobody can act on.
*/
func TestCompatibility(t *testing.T) {
	for _, c := range []struct {
		name             string
		version, buildID string
		level            version.Level
		message          string
	}{
		{
			name: "the build this targets", version: version.KnownVersion,
			buildID: version.KnownBuildID, level: version.Exact,
			message: "Terraria 1.4.5.8 (matches known-good build)",
		},
		{
			name: "a Steam rebuild of it", version: version.KnownVersion,
			buildID: "99999999", level: version.Hotfix,
			message: "version 1.4.5.8 matches but Steam buildid 99999999 != known " +
				"24893155; likely a rebuild, offsets probably fine but unverified",
		},
		{
			name: "no manifest to read", version: version.KnownVersion,
			level: version.Exact, message: "Terraria 1.4.5.8 (matches known-good build)",
		},
		{
			name: "a fourth-component hotfix", version: "1.4.5.9",
			buildID: version.KnownBuildID, level: version.Hotfix,
			message: "hotfix 1.4.5.9 vs known 1.4.5.8: offsets MIGHT still be valid " +
				"but are unproven on this build",
		},
		{
			name: "the same three, spelled shorter", version: "1.4.5",
			buildID: version.KnownBuildID, level: version.Hotfix,
			message: "hotfix 1.4.5 vs known 1.4.5.8: offsets MIGHT still be valid " +
				"but are unproven on this build",
		},
		{
			name: "a real update", version: "1.4.6.0", buildID: version.KnownBuildID,
			level: version.Incompatible,
			message: "Terraria 1.4.6.0 differs from 1.4.5.8 in major/minor/patch; " +
				"the offsets are almost certainly wrong",
		},
		{
			name: "the previous era", version: "1.3.5.3", level: version.Incompatible,
			message: "Terraria 1.3.5.3 differs from 1.4.5.8 in major/minor/patch; " +
				"the offsets are almost certainly wrong",
		},
		{
			name: "the runtime's own version", version: "2.0.50727",
			level: version.Incompatible,
			message: "Terraria 2.0.50727 differs from 1.4.5.8 in major/minor/patch; " +
				"the offsets are almost certainly wrong",
		},
		{
			name: "nothing readable yet", level: version.Unknown,
			message: "could not read the game version from memory",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			level, message := version.Compatibility(c.version, c.buildID)
			require.Equal(t, c.level, level, "classified differently")
			require.Equal(t, c.message, message, "explained differently")
		})
	}
}

/*
A version read out of memory, including the cases that once got it wrong.

The frequency vote is the fallback, and it is the part with history: the runtime
string outnumbering the game's at startup, and a single baked-in constant being
mistaken for the version being run.
*/
func TestDetectingFromMemory(t *testing.T) {
	for _, c := range []struct {
		name    string
		planted map[string]int // version string -> how many copies
		want    string
	}{
		{name: "nothing at all"},
		{
			name:    "one copy, which is a constant and not the running version",
			planted: map[string]int{"1.4.5.8": 1},
		},
		{
			name:    "two copies, which is the running version",
			planted: map[string]int{"1.4.5.8": 2}, want: "1.4.5.8",
		},
		{
			// What actually happens on this build: stale data outnumbers the truth.
			name:    "stale data outnumbering the real one",
			planted: map[string]int{"1.4.5.7": 4, "1.4.5.8": 2}, want: "1.4.5.7",
		},
		{
			// And the one that concluded the game was "Terraria 2.0.50727".
			name:    "the runtime's own version, which is not a game version",
			planted: map[string]int{"2.0.50727": 6},
		},
		{
			name:    "the runtime alongside the game",
			planted: map[string]int{"2.0.50727": 6, "1.4.5.8": 2}, want: "1.4.5.8",
		},
		{name: "a short version, which is not one", planted: map[string]int{"1.4": 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := version.Detect(plant(c.planted), -1)
			if c.want == "" {
				require.Empty(t, got, "a version was read where there is none")
				return
			}
			require.Equal(t, c.want, got, "a different version was read")
		})
	}
}

// noExe is a fake with no executable behind it, so only the memory scan runs.
type noExe struct{ *memtest.FakeMem }

func (noExe) ExePath() string { return "" }

// plant writes each version string into memory the given number of times, as
// the game stores it.
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
func TestReadingTheBuildID(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "steamapps", "common", "Terraria")
	require.NoError(t, os.MkdirAll(game, 0o755))
	exe := filepath.Join(game, "Terraria.exe")
	require.NoError(t, os.WriteFile(exe, []byte("MZ"), 0o600))

	manifest := filepath.Join(dir, "steamapps", "appmanifest_105600.acf")
	require.NoError(t, os.WriteFile(manifest, []byte(
		"\"AppState\"\n{\n\t\"buildid\"\t\t\"24893155\"\n}\n"), 0o600))

	for _, c := range []struct{ name, path, want string }{
		{name: "beside a manifest", path: exe, want: "24893155"},
		{name: "no path at all"},
		{
			name: "a path with no manifest",
			path: filepath.Join(dir, "elsewhere", "x", "y", "Terraria.exe"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := version.ReadBuildID(c.path)
			if c.want == "" {
				require.Empty(t, got, "a build id was read where there is none")
				return
			}
			require.Equal(t, c.want, got, "a different build id")
		})
	}
}

/*
The version in the game's own executable is the authority, and only when the
file holds exactly one plausible version.

A file with two is a file this cannot speak for -- and one of the two would be
picked, which is worse than admitting it does not know.
*/
func TestReadingTheVersionFromAnExecutable(t *testing.T) {
	for _, c := range []struct {
		name     string
		contents []string
		want     string
	}{
		{name: "one version", contents: []string{"1.4.5.8"}, want: "1.4.5.8"},
		{
			name: "the same one twice", contents: []string{"1.4.5.8", "1.4.5.8"},
			want: "1.4.5.8",
		},
		{name: "two different ones", contents: []string{"1.4.5.8", "1.4.5.7"}},
		{name: "none"},
		{
			name:     "one, beside the runtime's",
			contents: []string{"1.4.5.8", "2.0.50727"}, want: "1.4.5.8",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Terraria.exe")
			var body []byte
			for _, v := range c.contents {
				body = append(body, utf16le("v"+v)...)
				body = append(body, make([]byte, 16)...)
			}
			require.NoError(t, os.WriteFile(path, body, 0o600))

			got := version.VersionFromExe(path)
			if c.want == "" {
				require.Empty(t, got, "a version was read where there is none")
				return
			}
			require.Equal(t, c.want, got, "a different version was read from the file")
		})
	}
}
