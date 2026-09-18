package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

/*
One control panel at a time.

Two panels would mean two privileged workers, two inventory syncs and two
auto-restore loops racing on one game's patch state. The state file is locked so
it cannot be corrupted, but the panels still fight: one toggles a cheat and the
other's next status poll flips the box back.

The lock lives under XDG_RUNTIME_DIR rather than the config directory, because
the config directory is created by the CLI under sudo and is root-owned -- the
unprivileged window cannot write there. The kernel drops an flock when the holder
dies, so a crash or a SIGKILL cannot leave a stale lock behind.

The path is the Qt panel's, so the two front ends exclude each other rather than
each excluding only its own kind. During the port both are installed, and two
panels of different colours fight exactly as well as two of the same.
*/
func lockPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("terrariabonker-gui-%d.lock", os.Getuid()))
}

/*
takeLock claims the single-instance lock.

Returns a release, the holder's pid when somebody else has it, and whether this
process now owns it. The file stays open for as long as the lock is held:
closing it is what drops the lock, so the handle outlives this function.
*/
func takeLock(path string) (release func(), holder string, ok bool) {
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // a path from the runtime dir
	if err != nil {
		// Nowhere to put a lock is not a reason to refuse to start. The guard
		// is a courtesy against two panels; losing it is worse than a window
		// that will not open at all.
		return func() {}, "", true
	}
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held, _ := os.ReadFile(path) //nolint:gosec // the path this function just opened
		_ = fh.Close()
		return func() {}, strings.TrimSpace(string(held)), false
	}

	if err := fh.Truncate(0); err == nil {
		_, _ = fh.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}
	return func() { _ = fh.Close() }, "", true
}

// alreadyRunning is what the second panel is told. A sentence rather than a
// code, because it is shown to whoever double-clicked the icon.
func alreadyRunning(holder string) string {
	where := ""
	if holder != "" {
		where = " (pid " + holder + ")"
	}
	return "Another terrariabonker control panel is already open" + where + ".\n\n" +
		"Two of them would start two privileged workers and two auto-restore loops on " +
		"the same game, and they fight over the same patches. Use the window that is " +
		"already open."
}
