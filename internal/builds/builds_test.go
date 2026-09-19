package builds_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/builds"
)

/*
The decisions this machine has made about game builds.

The file is compared as bytes rather than as meaning. It is written indented and
key-sorted, and a rewrite in a different shape would show up in somebody's
config directory as every decision changing at once -- which is not wrong, but
is the kind of thing that should be a decision rather than a side effect of a
refactor.

Every file below is the one the implementation this was ported from wrote, while
both existed.
*/

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

// Remembering a decision writes the file out, byte for byte.
func TestRememberingWritesTheFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		how     string
		failed  []string
		runtime string
		want    string
	}{
		{
			name: "accepted, nothing failed", how: builds.Accepted,
			runtime: "wine-mono-11.2.0",
			want: `{
 "` + oneBuild + `": {
  "decision": "accepted",
  "failed": [],
  "runtime": "wine-mono-11.2.0"
 }
}`,
		},
		{
			name: "degraded, with failures", how: builds.Degraded,
			failed: []string{"teleport", "loot"}, runtime: "wine-mono-11.2.0",
			want: `{
 "` + oneBuild + `": {
  "decision": "degraded",
  "failed": [
   "loot",
   "teleport"
  ],
  "runtime": "wine-mono-11.2.0"
 }
}`,
		},
		{
			// A decision made before the runtime was tracked reports none,
			// which is the truth about it rather than a guess.
			name: "no runtime recorded", how: builds.Accepted,
			want: `{
 "` + oneBuild + `": {
  "decision": "accepted",
  "failed": []
 }
}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := atHome(t)
			require.NoError(t, builds.Remember(oneBuild, c.how, c.failed, c.runtime))
			require.Equal(t, c.want, read(t, path), "a differently shaped file")
		})
	}
}

// read is a file as it was written.
func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test made
	require.NoError(t, err)
	return string(raw)
}

// And a second decision joins the first rather than replacing the file.
func TestASecondDecision(t *testing.T) {
	path := atHome(t)
	require.NoError(t, builds.Remember(oneBuild, builds.Accepted, nil, "wine-mono-11.2.0"))
	require.NoError(t, builds.Remember(another, builds.Degraded, []string{"loot"}, ""))
	require.NoError(t, builds.Forget(oneBuild))
	require.NoError(t, builds.Remember(oneBuild, builds.Degraded, []string{"teleport"}, ""))

	require.Equal(t, `{
 "`+oneBuild+`": {
  "decision": "degraded",
  "failed": [
   "teleport"
  ]
 },
 "`+another+`": {
  "decision": "degraded",
  "failed": [
   "loot"
  ]
 }
}`, read(t, path), "a differently shaped file")
}

/*
A decision written out is read back as itself.

The file is the only thing between one run and the next, so a field renamed on
one side of it is a machine that forgets what it decided and asks again.
*/
func TestADecisionIsReadBack(t *testing.T) {
	atHome(t)
	require.NoError(t, builds.Remember(oneBuild, builds.Degraded,
		[]string{"teleport", "loot"}, "wine-mono-11.2.0"))

	got, known := builds.Get(oneBuild)
	require.True(t, known, "the decision was not found")
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
func TestForgettingOneBuild(t *testing.T) {
	path := atHome(t)
	require.NoError(t, builds.Remember(oneBuild, builds.Accepted, nil, ""))
	require.NoError(t, builds.Remember(another, builds.Degraded, []string{"loot"}, ""))
	require.NoError(t, builds.Forget(oneBuild))

	require.Equal(t, `{
 "`+another+`": {
  "decision": "degraded",
  "failed": [
   "loot"
  ]
 }
}`, read(t, path), "a differently shaped file")

	_, known := builds.Get(oneBuild)
	require.False(t, known, "the forgotten build is still decided")
	_, other := builds.Get(another)
	require.True(t, other, "forgetting one build took the other with it")
}
