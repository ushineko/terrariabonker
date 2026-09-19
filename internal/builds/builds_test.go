package builds_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/builds"
)

/*
The decisions this machine has made about game builds.

Both implementations read and write one file while both exist, so the file is
the contract -- and the comparison is the bytes rather than the meaning: the
Python writes it indented and key-sorted, and a rewrite in a different shape
would show up in the user's config directory as every decision changing at once.
*/

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// realHome is the home directory before the test moved it, so the Python child
// can still find its own packages.
var realHome = os.Getenv("HOME")

/*
pyBuilds runs a script against the same file.

The module path is pointed at the scratch file rather than HOME being moved,
because moving HOME costs the child its own packages and turns a comparison into
a silent skip.
*/
func pyBuilds(t *testing.T, path, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import builds
builds._PATH = `+quote(path)+`
`+script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	return string(out)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// atHome puts the config directory somewhere disposable and says where.
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".config", "terrariabonker", "accepted-builds.json")
}

const (
	oneBuild = "1.4.5.7+24893155"
	another  = "1.4.5.8+25000000"
)

// Remembering a decision writes the same file, byte for byte.
func TestRememberingMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name    string
		how     string
		failed  []string
		runtime string
		python  string
	}{
		{name: "accepted, nothing failed", how: builds.Accepted, failed: nil,
			runtime: "wine-mono-11.2.0",
			python:  `builds.remember(KEY, builds.ACCEPTED, (), runtime="wine-mono-11.2.0")`},
		{name: "degraded, with failures", how: builds.Degraded,
			failed: []string{"teleport", "loot"}, runtime: "wine-mono-11.2.0",
			python: `builds.remember(KEY, builds.DEGRADED, ("teleport", "loot"), runtime="wine-mono-11.2.0")`},
		{
			// A decision made before the runtime was tracked reports none,
			// which is the truth about it rather than a guess.
			name: "no runtime recorded", how: builds.Accepted, failed: nil, runtime: "",
			python: `builds.remember(KEY, builds.ACCEPTED, ())`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pyPath := filepath.Join(t.TempDir(), "accepted-builds.json")
			pyBuilds(t, pyPath, "KEY = "+quote(oneBuild)+"\n"+c.python)
			want, err := os.ReadFile(pyPath)
			require.NoError(t, err)

			path := atHome(t)
			require.NoError(t, builds.Remember(oneBuild, c.how, c.failed, c.runtime))
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got), "a differently shaped file")
		})
	}
}

// And a second decision joins the first rather than replacing the file.
func TestASecondDecisionMatchesThePython(t *testing.T) {
	script := "KEY = " + quote(oneBuild) + "\nOTHER = " + quote(another) + `
builds.remember(KEY, builds.ACCEPTED, (), runtime="wine-mono-11.2.0")
builds.remember(OTHER, builds.DEGRADED, ("loot",))
builds.forget(KEY)
builds.remember(KEY, builds.DEGRADED, ("teleport",))
`
	pyPath := filepath.Join(t.TempDir(), "accepted-builds.json")
	pyBuilds(t, pyPath, script)
	want, err := os.ReadFile(pyPath)
	require.NoError(t, err)

	path := atHome(t)
	require.NoError(t, builds.Remember(oneBuild, builds.Accepted, nil, "wine-mono-11.2.0"))
	require.NoError(t, builds.Remember(another, builds.Degraded, []string{"loot"}, ""))
	require.NoError(t, builds.Forget(oneBuild))
	require.NoError(t, builds.Remember(oneBuild, builds.Degraded, []string{"teleport"}, ""))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "a differently shaped file")
}

// What the Python wrote, this reads back as the same decision.
func TestReadingBackWhatThePythonWrote(t *testing.T) {
	path := atHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	pyBuilds(t, path, "KEY = "+quote(oneBuild)+`
builds.remember(KEY, builds.DEGRADED, ("teleport", "loot"), runtime="wine-mono-11.2.0")`)

	got, known := builds.Get(oneBuild)
	require.True(t, known, "the decision the Python recorded was not found")
	require.Equal(t, builds.Decision{Decision: builds.Degraded,
		Failed: []string{"loot", "teleport"}, Runtime: "wine-mono-11.2.0"}, got)
	require.Equal(t, map[string]bool{"loot": true, "teleport": true},
		builds.FailedCheats(oneBuild))
}

// A build nobody has decided about is unknown rather than empty.
func TestAnUndecidedBuild(t *testing.T) {
	atHome(t)
	_, known := builds.Get(oneBuild)
	require.False(t, known, "an undecided build came back as decided")
	require.Empty(t, builds.FailedCheats(oneBuild))
	require.Empty(t, builds.Load())
}

/*
Forgetting a build nobody decided about writes nothing at all.

Not a nicety: a rewrite would create the file on a machine that has never made a
decision, and a file full of nothing then reads as "asked and answered".
*/
func TestForgettingAnUndecidedBuildWritesNothing(t *testing.T) {
	path := atHome(t)
	require.NoError(t, builds.Forget(oneBuild))
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "a file was written for no decision")
}

// A file that is not a file of decisions is ignored rather than trusted.
func TestGarbageDecisions(t *testing.T) {
	path := atHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	for _, junk := range []string{`["not", "a", "map"]`, `null`, `{`} {
		require.NoError(t, os.WriteFile(path, []byte(junk), 0o600))
		require.Emptyf(t, builds.Load(), "%s was read as a set of decisions", junk)

		// And writing over it still produces a file the other side can read.
		require.NoError(t, builds.Remember(oneBuild, builds.Accepted, nil, ""))
		got, known := builds.Get(oneBuild)
		require.True(t, known)
		require.Equal(t, builds.Accepted, got.Decision)
	}
}

/*
Forgetting one build leaves the others, and asks again about that one.

Separate from the round above, where the forgotten build is recorded again
immediately: there, a forget that did nothing at all is invisible.
*/
func TestForgettingOneBuildMatchesThePython(t *testing.T) {
	script := "KEY = " + quote(oneBuild) + "\nOTHER = " + quote(another) + `
builds.remember(KEY, builds.ACCEPTED, ())
builds.remember(OTHER, builds.DEGRADED, ("loot",))
builds.forget(KEY)
`
	pyPath := filepath.Join(t.TempDir(), "accepted-builds.json")
	pyBuilds(t, pyPath, script)
	want, err := os.ReadFile(pyPath)
	require.NoError(t, err)

	path := atHome(t)
	require.NoError(t, builds.Remember(oneBuild, builds.Accepted, nil, ""))
	require.NoError(t, builds.Remember(another, builds.Degraded, []string{"loot"}, ""))
	require.NoError(t, builds.Forget(oneBuild))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "a differently shaped file")

	_, known := builds.Get(oneBuild)
	require.False(t, known, "the forgotten build is still decided")
	_, other := builds.Get(another)
	require.True(t, other, "forgetting one build took the other with it")
}
