package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
