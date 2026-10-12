package gui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// plantCLI puts a file exec.LookPath takes for the CLI into dir, and returns
// its path.
func plantCLI(t *testing.T, dir string) string {
	t.Helper()
	name := cliName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)) //nolint:gosec // a test's stand-in for a program has to be executable
	return path
}

/*
The window drives the CLI installed beside it, and PATH's only without one.

Both are on PATH after any install, so PATH alone picks whichever directory
comes first: a window from the build tree drove the installed CLI of the release
before (spec 052, slice 28), and a package and a per-user install on one machine
are two CLIs and two windows. The real exec.LookPath is used, against real
files, because what it accepts as a program differs by platform.
*/
func TestTheWindowDrivesTheCLIBesideIt(t *testing.T) {
	beside, onPath, empty := t.TempDir(), t.TempDir(), t.TempDir()
	neighbour := plantCLI(t, beside)
	other := plantCLI(t, onPath)
	t.Setenv("PATH", onPath)

	at := func(dir string) func() (string, error) {
		return func() (string, error) { return filepath.Join(dir, "terrariabonker-gui"), nil }
	}

	require.Equal(t, neighbour, findCLI(at(beside), exec.LookPath),
		"with a CLI beside the window, the one on PATH was driven")
	require.Equal(t, other, findCLI(at(empty), exec.LookPath),
		"with no CLI beside the window, the one on PATH was not found")
	require.Equal(t, other, findCLI(func() (string, error) { return "", errors.New("no path") }, exec.LookPath),
		"a window that cannot name its own file did not fall back to PATH")

	t.Setenv("PATH", empty)
	require.Empty(t, findCLI(at(empty), exec.LookPath), "a CLI was found where there is none")
}
