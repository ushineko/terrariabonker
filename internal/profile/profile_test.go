package profile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/profile"
)

/*
The profile is the one file both implementations write for each other.

While both exist the window can reach either, and this is what the auto-restore
reads when a fresh game turns up. A profile written by one and misread by the
other is a restore that puts back the wrong thing, or nothing -- and the player
finds out by discovering their cheats are off.

So every case round-trips both ways.
*/

// atHome points Go at a scratch profile, so a test never touches the real one.
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

/*
Everything saved into a profile comes back out of it.

The profile is the only thing between one game and the next: it is what
auto-restore re-applies, so a field that does not survive the file is a cheat
that quietly stops coming back.
*/
func TestAProfileIsReadBack(t *testing.T) {
	atHome(t)

	twenty := 20.0
	require.NoError(t, profile.SetCheat("reach", true, &twenty))
	require.NoError(t, profile.SetCheat("pylons", true, nil))
	require.NoError(t, profile.SetItemEdit(3509, map[string]float64{
		"damage": 500, "pick": 200,
		// Not restorable: the game writes it into the save itself.
		"prefix": 81,
	}))
	require.NoError(t, profile.ClearItem(7))
	require.NoError(t, profile.SetSellWhitelist(9, true))
	require.NoError(t, profile.RememberRodPower(2294, 25))

	cheats := profile.Cheats()
	require.Len(t, cheats, 2, "a cheat did not survive")
	require.NotNil(t, cheats["reach"])
	require.InDelta(t, 20.0, *cheats["reach"], 0)
	require.Nil(t, cheats["pylons"], "a valueless cheat came back with a value")

	require.Equal(t, map[int32]map[string]float64{3509: {"damage": 500, "pick": 200}},
		profile.ItemEdits(),
		"a non-restorable field was saved, or a restorable one lost")
	require.Equal(t, []int{7}, profile.EmptySlots())
	require.Equal(t, []int32{9}, profile.SellWhitelist())
	require.Equal(t, map[int32]int32{2294: 25}, profile.RodPowersToRestore())
}

/*
A rod's original power is recorded once and never overwritten.

Switch the cheat on twice without a restore in between and the second call would
otherwise record the *cheated* power as the original, so the rod could never be
put back.
*/
func TestARodsOriginalPowerIsRecordedOnce(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.RememberRodPower(2294, 25))
	require.NoError(t, profile.RememberRodPower(2294, 200))
	require.Equal(t, map[int32]int32{2294: 25}, profile.RodPowersToRestore(),
		"the cheated power was recorded as the original")

	require.NoError(t, profile.ForgetRodPower(2294))
	require.Empty(t, profile.RodPowersToRestore(), "the note survived being forgotten")
}

/*
An old slot-keyed profile is folded into the type-keyed one.

Edits used to be stored per slot, which meant an item that moved lost its edit
and an item whose only change was a prefix was reported as a failure on every
launch.
*/
func TestAnOldProfileIsMigratedTheSameWay(t *testing.T) {
	const legacy = `{"items": {"0": {"type": 3509, "damage": 500, "prefix": 81},
	                           "7": {"type": 0},
	                           "9": {"type": 9, "stack": 99}}}`

	home := atHome(t)
	writeProfile(t, home, legacy)
	got := profile.Load()

	require.Equal(t, map[string]map[string]float64{"3509": {"damage": 500}}, got.ItemEdits,
		"the edits were folded differently")
	require.Equal(t, []int{7}, got.EmptySlots, "the emptied slots differ")
	require.Nil(t, got.Items, "the old shape was kept")

	// An item whose only saved field is one the game regenerates carries nothing
	// worth restoring, so it does not become an entry at all.
	require.NotContains(t, got.ItemEdits, "9", "an item with nothing restorable was saved")
}

// A missing or unreadable profile is an empty one, not a failure: nothing in it
// is worth failing a launch over.
func TestABadProfileIsEmptyRatherThanFatal(t *testing.T) {
	home := atHome(t)
	require.NotNil(t, profile.Load().Cheats, "a missing profile is not usable")

	for _, bad := range []string{``, `{`, `{"cheats": "not a map"}`, `[]`} {
		writeProfile(t, home, bad)
		got := profile.Load()
		require.NotNilf(t, got.Cheats, "%q did not give a usable empty profile", bad)
		require.NotNil(t, got.ItemEdits)
		require.NotNil(t, got.FishingRestore)
		require.Emptyf(t, got.Cheats, "%q was read as having cheats", bad)
	}
}

// Nothing here writes to the real profile.
func TestTheTestsNeverTouchTheRealProfile(t *testing.T) {
	home := atHome(t)
	require.Contains(t, profile.Path(), home)
}

func writeProfile(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".config", "terrariabonker", "profile.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

/*
An edit with nothing worth restoring removes the entry rather than storing an
empty one.

Every field the caller submitted may be one the game writes into the save itself,
and an entry with no fields left is a promise to restore nothing -- which
auto-restore then reports on, every launch, about an item that was never wrong.
*/
func TestAnEditWithNothingRestorableRemovesTheEntry(t *testing.T) {
	atHome(t)

	require.NoError(t, profile.SetItemEdit(3509, map[string]float64{"damage": 500}))
	require.NotEmpty(t, profile.ItemEdits(), "nothing was stored to begin with")

	require.NoError(t, profile.SetItemEdit(3509, map[string]float64{"prefix": 81, "stack": 99}))
	require.Empty(t, profile.ItemEdits(), "an entry with nothing restorable was kept")
}
