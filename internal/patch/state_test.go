package patch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
The two implementations share one state file, so it has to round-trip both ways.

This is not a format the port may quietly change. While both exist the window can
reach either one, and a record written by Go and read by the Python -- or the
other way about -- decides where a stub is and whether it is installed. Losing
that is a process full of live stubs that nothing remembers how to remove: no
disable will work, and only restarting the game clears them.

So a record is written by each and read by the other.
*/

/*
atHome points both implementations at a scratch state file, so a test never
touches the maintainer's real patch state.

Go finds its path through HOME, which is set for this process only. The Python
child keeps the real one -- see the note beside realHome -- and is pointed at the
same file by name instead.
*/
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// pyState is the Python reading or writing the same file.
func pyState(home string) string {
	return fmt.Sprintf(`
import json, os, sys
os.environ["HOME"] = %q
sys.path.insert(0, os.getcwd())
from terrariabonker import patcher as P
P._STATE = os.path.join(%q, ".config", "terrariabonker", "patches.json")
p = P.Patcher.__new__(P.Patcher)
p.mem = type("M", (), {"pid": 4242})()
p._sites, p._enabled, p._inj, p._values, p._arena = {}, set(), {}, {}, None
`, realHome, home)
}

/*
A record naming another process is not used.

Those addresses belong to a game that has exited. Writing to them lands in
whatever occupies that memory now, which on a 32-bit process with a recycled
address space is somebody else's live data.
*/
func TestAStateForAnotherProcessIsIgnored(t *testing.T) {
	atHome(t)
	require.NoError(t, patch.State{
		PID: 4242, Arena: 0x68000000, Enabled: []string{"loot"},
	}.Save())

	same := patch.LoadState(4242)
	require.Equal(t, uint32(0x68000000), same.Arena, "the state for this process was discarded")

	other := patch.LoadState(9999)
	require.Zero(t, other.Arena, "another process's arena was adopted")
	require.Empty(t, other.Enabled, "another process's cheats were believed")
	require.NotNil(t, other.Sites, "and the empty state is not usable")
}

/*
An unreadable or half-written record is an empty one, not a failure.

Everything in it can be recovered by scanning again, so there is nothing here
worth failing an operation over -- and the window asks for status on a timer,
which is not a place to surface a parse error.
*/
func TestABadStateIsEmptyRatherThanFatal(t *testing.T) {
	atHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(patch.StatePath()), 0o755))
	for _, bad := range []string{
		// A write interrupted part-way.
		`{"pid": 4242, "enabled": ["loot"], "arena":`,
		// Valid JSON of the wrong shape, which is what a future version writing
		// a field differently would leave behind. This one decodes the fields
		// in front of the mistake before it fails, so taking what parsed would
		// report one cheat on and the rest off -- and the window would offer to
		// enable stubs that are already live.
		`{"pid": 4242, "enabled": ["loot"], "sites": "not a map"}`,
	} {
		require.NoError(t, os.WriteFile(patch.StatePath(), []byte(bad), 0o644))

		got := patch.LoadState(4242)
		require.Emptyf(t, got.Enabled, "half a record was read as a whole one: %s", bad)
		require.NotNilf(t, got.Sites, "%s did not give a usable empty state", bad)
		require.NotNil(t, got.Inj)
		require.NotNil(t, got.Values)
	}
}

/*
A mutating operation re-reads the record under the lock.

The window applies several cheats one process at a time. Without the re-read, the
second process saves the state it loaded before the first had written, so a cheat
is live in the game and the record says it is not -- and nothing can turn it off
again.
*/
func TestALockedOperationSeesWhatWasWrittenWhileItWaited(t *testing.T) {
	atHome(t)
	require.NoError(t, patch.State{PID: 4242, Enabled: []string{"loot"}}.Save())

	require.NoError(t, patch.WithLock(4242, func(s *patch.State) error {
		require.Equal(t, []string{"loot"}, s.Enabled, "the lock handed over a stale record")
		s.SetEnabled("reach", true)
		return nil
	}))

	require.Equal(t, []string{"loot", "reach"}, patch.LoadState(4242).Enabled)
}

// A failed operation leaves the record as it was.
func TestAFailedOperationDoesNotWriteTheRecord(t *testing.T) {
	atHome(t)
	require.NoError(t, patch.State{PID: 4242, Enabled: []string{"loot"}}.Save())

	err := patch.WithLock(4242, func(s *patch.State) error {
		s.SetEnabled("reach", true)
		return fmt.Errorf("the game said no")
	})
	require.Error(t, err)
	require.Equal(t, []string{"loot"}, patch.LoadState(4242).Enabled,
		"a cheat that was not applied was recorded as on")
}
