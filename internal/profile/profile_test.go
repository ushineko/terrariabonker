package profile_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// realHome is the maintainer's own, kept before any test moves it: the Python
// child needs it to find its own packages.
var realHome = os.Getenv("HOME")

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

// atHome points Go at a scratch profile, so a test never touches the real one.
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// pyProfile is the Python pointed at the same file.
func pyProfile(home string) string {
	return fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import profile
profile._PATH = os.path.join(%q, ".config", "terrariabonker", "profile.json")
`, home)
}

// A profile Go wrote is one the Python reads, field for field.
func TestThePythonReadsAProfileGoWrote(t *testing.T) {
	home := atHome(t)

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

	var got map[string]any
	askPython(t, pyProfile(home)+`
print(json.dumps({"cheats": profile.cheats(),
                  "item_edits": {str(k): v for k, v in profile.item_edits().items()},
                  "empty_slots": profile.empty_slots(),
                  "whitelist": sorted(profile.sell_whitelist()),
                  "rods": {str(k): v for k, v in profile.rod_powers_to_restore().items()}}))`, &got)

	require.Equal(t, map[string]any{"reach": 20.0, "pylons": nil}, got["cheats"],
		"the cheats did not survive")
	require.Equal(t, map[string]any{"3509": map[string]any{"damage": 500.0, "pick": 200.0}},
		got["item_edits"], "a non-restorable field was saved, or a restorable one lost")
	require.Equal(t, []any{7.0}, got["empty_slots"])
	require.Equal(t, []any{9.0}, got["whitelist"])
	require.Equal(t, map[string]any{"2294": 25.0}, got["rods"])
}

// And a profile the Python wrote is one Go reads.
func TestGoReadsAProfileThePythonWrote(t *testing.T) {
	home := atHome(t)

	var ok bool
	askPython(t, pyProfile(home)+`
profile.set_cheat("reach", True, 20)
profile.set_cheat("pylons", True, None)
profile.set_item_edit(3509, {"damage": 500, "pick": 200, "prefix": 81})
profile.clear_item(7)
profile.set_sell_whitelist(9, True)
profile.remember_rod_power(2294, 25)
print(json.dumps(True))`, &ok)
	require.True(t, ok)

	cheats := profile.Cheats()
	require.Len(t, cheats, 2)
	require.NotNil(t, cheats["reach"])
	require.InDelta(t, 20.0, *cheats["reach"], 0)
	require.Nil(t, cheats["pylons"], "a valueless cheat came back with a value")

	require.Equal(t, map[int32]map[string]float64{3509: {"damage": 500, "pick": 200}},
		profile.ItemEdits(), "the edits differ")
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
	home := atHome(t)

	var want map[string]any
	askPython(t, pyProfile(home)+`
profile.remember_rod_power(2294, 25)
profile.remember_rod_power(2294, 200)
print(json.dumps({"rods": {str(k): v for k, v in profile.rod_powers_to_restore().items()}}))`,
		&want)
	require.Equal(t, map[string]any{"2294": 25.0}, want["rods"],
		"the Python overwrote a remembered power")

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

	var want map[string]any
	askPython(t, pyProfile(home)+`
d = profile.load()
print(json.dumps({"item_edits": d["item_edits"], "empty_slots": d["empty_slots"],
                  "items_gone": "items" not in d}))`, &want)

	atHome(t)
	writeProfile(t, os.Getenv("HOME"), legacy)
	got := profile.Load()

	require.Equal(t, want["item_edits"], asJSON(t, got.ItemEdits),
		"the edits were folded differently")
	require.Equal(t, want["empty_slots"], asJSON(t, got.EmptySlots),
		"the emptied slots differ")
	require.True(t, want["items_gone"].(bool), "the Python kept the old shape")
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

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

/*
An edit with nothing worth restoring removes the entry rather than storing an
empty one.

Every field the caller submitted may be one the game writes into the save itself,
and an entry with no fields left is a promise to restore nothing -- which
auto-restore then reports on, every launch, about an item that was never wrong.
*/
func TestAnEditWithNothingRestorableRemovesTheEntry(t *testing.T) {
	home := atHome(t)

	var want map[string]any
	askPython(t, pyProfile(home)+`
profile.set_item_edit(3509, {"damage": 500})
before = dict(profile.item_edits())
profile.set_item_edit(3509, {"prefix": 81, "stack": 99})
print(json.dumps({"before": {str(k): v for k, v in before.items()},
                  "after": {str(k): v for k, v in profile.item_edits().items()}}))`, &want)
	require.NotEmpty(t, want["before"], "the Python stored nothing to begin with")
	require.Empty(t, want["after"], "the Python kept an entry with nothing restorable")

	atHome(t)
	require.NoError(t, profile.SetItemEdit(3509, map[string]float64{"damage": 500}))
	require.NotEmpty(t, profile.ItemEdits(), "nothing was stored to begin with")

	require.NoError(t, profile.SetItemEdit(3509, map[string]float64{"prefix": 81, "stack": 99}))
	require.Empty(t, profile.ItemEdits(), "an entry with nothing restorable was kept")
}
