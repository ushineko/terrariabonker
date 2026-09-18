package gui

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
One panel at a time, and the second is told which one has it.

Two panels mean two privileged workers and two auto-restore loops racing on one
game's patch state. The lock is a real flock rather than a pid file someone
checks: the kernel drops it when the holder dies, so a crash cannot leave a
stale lock that keeps the panel from ever opening again.
*/
func TestOnlyOnePanelCanHoldTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.lock")

	release, holder, ok := takeLock(path)
	require.True(t, ok)
	require.Empty(t, holder)

	held, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(os.Getpid()), string(held),
		"the holder records itself, so the second panel can name it")

	release()

	// Released, so it can be taken again: closing the file is what drops it.
	release, holder, ok = takeLock(path)
	require.True(t, ok)
	require.Empty(t, holder)
	release()
}

// Nowhere to put a lock is not a reason to refuse to start. The guard is a
// courtesy; losing it is better than a window that will not open at all.
func TestAnUnwritableLockPathDoesNotStopThePanel(t *testing.T) {
	release, _, ok := takeLock(filepath.Join(t.TempDir(), "no", "such", "dir", "panel.lock"))
	require.True(t, ok)
	release()
}

// The refusal names the holder when it recorded one, and reads as a sentence
// either way: it is shown to whoever double-clicked the icon.
func TestTheRefusalNamesTheOtherPanel(t *testing.T) {
	require.Contains(t, alreadyRunning("4242"), "already open (pid 4242).")
	require.Contains(t, alreadyRunning(""), "already open.")
	require.Contains(t, alreadyRunning(""), "Use the window that is already open.")
}

// The lock belongs in the runtime directory, not the config directory: the CLI
// creates that one under sudo and the unprivileged window cannot write there.
func TestTheLockLivesWhereTheUnprivilegedPanelCanWrite(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234")
	require.Equal(t, "/run/user/1234/terrariabonker-gui-"+strconv.Itoa(os.Getuid())+".lock",
		lockPath())

	t.Setenv("XDG_RUNTIME_DIR", "")
	require.Equal(t, filepath.Join(os.TempDir(),
		"terrariabonker-gui-"+strconv.Itoa(os.Getuid())+".lock"), lockPath())
}
