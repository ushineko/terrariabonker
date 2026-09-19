package cli_test

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

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/gui/client"
	"github.com/ushineko/terrariabonker/internal/patch"
)

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// realHome is the home directory before a test moved it, so the Python child
// can still find its own packages.
var realHome = os.Getenv("HOME")

/*
Every argv the window can emit is one this command line accepts.

This is the contract between two programs that do not share a line of code: the
window builds an argv and a privileged subprocess runs it, and a missing --json
or a renamed subcommand is a runtime gap with nothing to catch it. Checking the
declared subcommand as well as the parse is deliberate -- a builder that
switched to a different valid command would otherwise pass.
*/
func TestEveryArgvTheWindowEmitsIsAccepted(t *testing.T) {
	for _, sample := range client.Samples() {
		t.Run(sample.Name, func(t *testing.T) {
			if notYetPorted[sample.Cmd] {
				t.Skipf("%s arrives with the sprite extractor (spec 051, step 8)", sample.Cmd)
			}
			root := cli.Root()
			cmd, rest, err := root.Find(sample.Argv)
			require.NoErrorf(t, err, "%v reaches no command", sample.Argv)
			require.Equalf(t, sample.Cmd, cmd.Name(),
				"%v reaches %s rather than %s", sample.Argv, cmd.Name(), sample.Cmd)
			require.NoErrorf(t, cmd.ParseFlags(rest), "%v has a flag this does not take",
				sample.Argv)
			require.NoErrorf(t, cmd.ValidateArgs(cmd.Flags().Args()),
				"%v has arguments this does not take", sample.Argv)
		})
	}
}

/*
notYetPorted is the one command the window can ask for that this does not have
yet.

Icon extraction is unprivileged disk work and comes with the sprite reader. The
entry is here rather than in a comment so it disappears the moment the command
exists.
*/
var notYetPorted = map[string]bool{"extract-sprites": true}

/*
The two front ends accept and refuse the same argv.

The window's builders are one source of argv; a person typing is the other, and
that half has no test at all unless it is compared with the parser it replaces.
Both the accepting and the refusing matter: a command line that quietly ignored
an unknown flag would run the wrong operation and say it worked.
*/
func TestTheArgvSurfaceMatchesThePython(t *testing.T) {
	argvs := [][]string{
		{"status"}, {"status", "--json"},
		{"version"},
		{"inventory"}, {"inventory", "--all", "--json"}, {"inv", "--json"},
		{"set-hp", "max"}, {"set-hp", "300", "--force"},
		{"set-max-hp", "400"}, {"set-mana", "max"}, {"set-max-mana", "200"},
		{"set-stack", "3", "99"},
		{"set-item", "3", "29"},
		{"set-item", "3", "29", "--stack", "1", "--damage", "50", "--auto-reuse", "1",
			"--use-time", "10", "--use-anim", "10", "--pick", "100",
			"--tile-boost", "5", "--defense", "2", "--prefix", "27",
			"--expect-type", "29"},
		{"give", "29"}, {"give", "29", "--stack", "50"},
		{"fast-mining"}, {"fast-mining", "--use-time", "4"},
		{"long-reach"}, {"long-reach", "--tiles", "40"},
		{"compendium"}, {"compendium", "--json", "--refresh"},
		{"names"}, {"prefixes"}, {"recipes"},
		{"vein"}, {"vein", "10", "12", "--gems", "--limit", "20", "--map", "--json"},
		{"vein", "10", "12", "--orthogonal"},
		{"extract"}, {"extract", "10", "12", "--timeout", "5", "--json"},
		{"extract", "--watch", "--rounds", "3"},
		{"extract-tick", "--budget", "0.1", "--json"}, {"extract-stop", "--json"},
		{"potions"}, {"potions", "--watch", "--min-stack", "2", "--ticks", "600"},
		{"fishing"}, {"fishing", "--no-kit", "--keep", "50", "--json"},
		{"fishing", "--power", "255"}, {"fishing", "--restore"},
		{"fishing-buffs", "--power", "--sonar", "--crate"},
		{"catch", "--recast"}, {"catch-tick", "--json"}, {"catch-stop", "--json"},
		{"projectile-tick", "--set", "837:tileCollide=0", "--json"},
		{"projectile-stop", "--json"}, {"projectile-of", "3507", "--json"},
		{"sell", "--dry-run"}, {"sell", "--list", "--json"},
		{"sell-tick", "--json"}, {"sell-list", "--json"},
		{"build-check", "--json"}, {"accept-build", "accepted"},
		{"accept-build", "degraded", "--failed", "loot"},
		{"spawn-npc", "1", "--distance", "10", "--json"},
		{"patch", "catalog", "--json"}, {"patch", "status", "--json"},
		{"patch", "enable", "mining"}, {"patch", "on", "mining", "--value", "0.2"},
		{"patch", "disable", "mining"}, {"patch", "off", "mining"},
		{"freeze", "--godmode", "--mana"}, {"godmode", "--seconds", "5"},
		{"restore", "--json"}, {"extract-recipes"}, {"serve"},
		{"read", "0x10000000"}, {"write", "0x10000000", "5"},

		// And the ones both must refuse.
		{"not-a-command"},
		{"status", "--nonsense"},
		{"set-hp"},
		{"accept-build", "maybe"},
		{"patch", "sideways"},
		{"patch", "enable", "not-a-cheat"},
		{"accept-build", "accepted", "junk"},
		{"spawn-npc"},
	}
	want := pythonAccepts(t, argvs)
	for i, argv := range argvs {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			require.Equalf(t, want[i], goAccepts(argv),
				"the two disagree about whether %v is a command line", argv)
		})
	}
}

// goAccepts reports whether the tree would run an argv, without running it.
func goAccepts(argv []string) bool {
	root := cli.Root()
	cmd, rest, err := root.Find(argv)
	if err != nil || cmd == root {
		return false
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return false
	}
	return cmd.ValidateArgs(cmd.Flags().Args()) == nil
}

// pythonAccepts asks the parser this replaces the same question.
func pythonAccepts(t *testing.T, argvs [][]string) []bool {
	t.Helper()
	raw, err := json.Marshal(argvs)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import contextlib, io, json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker.cli import build_parser
parser = build_parser()
out = []
for argv in json.loads(sys.stdin.read()):
    buf = io.StringIO()
    try:
        with contextlib.redirect_stdout(buf), contextlib.redirect_stderr(buf):
            args = parser.parse_args(argv)
        out.append(getattr(args, "func", None) is not None)
    except SystemExit:
        out.append(False)
print(json.dumps(out))
`)
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(string(raw))
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	got, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(got), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", got)

	var accepts []bool
	require.NoError(t, json.Unmarshal(got, &accepts))
	require.Len(t, accepts, len(argvs))
	return accepts
}

/*
Every command the worker will run is a command that exists.

The allowlist is written out by hand, because deriving it from the tree would
allow whatever the tree happens to have -- which is the opposite of an
allowlist. What it must not do is name something that is not there: an entry
with no command behind it is an operation the window can ask for and never get.
*/
func TestEveryServedCommandExists(t *testing.T) {
	root := cli.Root()
	for name := range cli.ServeOps {
		cmd, _, err := root.Find([]string{name})
		require.NoErrorf(t, err, "%s is served but is not a command", name)
		require.NotEqualf(t, root, cmd, "%s is served but is not a command", name)
	}
	// And the dangerous ones are not in it.
	for _, name := range []string{"serve", "freeze", "godmode", "read", "write",
		"extract-recipes", "extract-sprites"} {
		require.Falsef(t, cli.ServeOps[name], "%s is served and must not be", name)
	}
}

// Every patch in the catalog can be named on the command line.
func TestEveryPatchCanBeToggled(t *testing.T) {
	root := cli.Root()
	cmd, _, err := root.Find([]string{"patch"})
	require.NoError(t, err)
	for _, info := range patch.Catalog() {
		require.NoError(t, cmd.ValidateArgs([]string{"enable", info.Name}),
			"%s cannot be turned on", info.Name)
	}
}
