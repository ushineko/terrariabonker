package trainer

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/player"
)

/*
Holding a value against a game that recomputes it every frame.

Everything here is about the loop noticing rather than about the write, which
the player package already covers: a pass that restored nothing is not the same
as a pass that could not read, and telling them apart is what makes a world
reload recoverable instead of fatal.
*/

const (
	base = 0x20000000
	life = base + 0x1000
)

func fake(t *testing.T, block []int32) *memtest.FakeMem {
	t.Helper()
	m := memtest.New(base, 0x4000)
	m.PlantPlayer(life, block, 0)
	return m
}

// pyTrainer puts the same question to the Python.
func pyTrainer(t *testing.T, script string, into any) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))

	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker.player import Player
from terrariabonker.trainer import Freezer
BASE = `+itoa(base)+`
LIFE = `+itoa(life)+`
`+script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

/*
A pass puts a value that has moved back, and counts it.

The count is what the command line prints when the loop ends, so it is the only
evidence a person has that anything was held at all.
*/
func TestATickRestoresLifeMatchingThePython(t *testing.T) {
	var want struct {
		Life  int32 `json:"life"`
		Saves int   `json:"saves"`
		OK    bool  `json:"ok"`
	}
	pyTrainer(t, `
m = FakeMem(BASE, 0x4000)
m.plant_player(LIFE, [100, 100, 100, 20, 20, 20], name_ptr=0)
fr = Freezer(m, godmode=True)
fr.players = [Player(m, LIFE)]
Player(m, LIFE).set_life(30)
ok = fr._tick()
print(json.dumps({"life": Player(m, LIFE).stat_life, "saves": fr.saves, "ok": ok}))`, &want)

	mem := fake(t, []int32{100, 100, 100, 20, 20, 20})
	f := New(mem, true, false, DefaultHz)
	f.players = []*player.Player{player.New(mem, life)}
	player.New(mem, life).SetLife(30) // a hit lands

	require.Equal(t, want.OK, f.Tick(), "a different verdict on the pass")
	got, _ := player.New(mem, life).StatLife()
	require.Equal(t, want.Life, got, "a different life was left behind")
	require.Equal(t, want.Saves, f.Saves, "a different number of restores was counted")
	require.EqualValues(t, 100, got, "the hit was not undone")
}

/*
A value already at its cap is left alone and not counted.

Counting it would report the loop working when it is idle, which is what the
count is read for.
*/
func TestATickThatHasNothingToDo(t *testing.T) {
	mem := fake(t, []int32{100, 100, 100, 20, 20, 20})
	f := New(mem, true, false, DefaultHz)
	f.players = []*player.Player{player.New(mem, life)}

	require.True(t, f.Tick(), "a readable player was reported as stale")
	require.Zero(t, f.Saves, "a value already at its cap was counted as a restore")
}

/*
A pass that could not read anything is stale, which is how a world reload is
noticed.

A reload leaves the addresses pointing at nothing, and the loop has to tell that
from a pass with nothing to do -- those are the same number of writes and
opposite situations.
*/
func TestATickOnDeadAddressesMatchesThePython(t *testing.T) {
	var want bool
	pyTrainer(t, `
m = FakeMem(BASE, 0x4000)
m.plant_player(LIFE, [100, 100, 80, 20, 15, 20], name_ptr=0)
fr = Freezer(m, godmode=True)
fr.players = [Player(m, BASE + 0x999999)]
print(json.dumps(fr._tick()))`, &want)

	mem := fake(t, []int32{100, 100, 80, 20, 15, 20})
	f := New(mem, true, false, DefaultHz)
	f.players = []*player.Player{player.New(mem, base+0x999999)}

	require.Equal(t, want, f.Tick(), "a different verdict on dead addresses")
	require.False(t, f.Tick(), "an unreadable player was reported as fine")
}

/*
Freezing life does not quietly freeze mana too.

The two levers are separate on the command line and in the panel, and a freezer
that held both would make the mana switch look broken by doing its job for it.
*/
func TestGodmodeLeavesManaAlone(t *testing.T) {
	mem := fake(t, []int32{100, 100, 100, 5, 20, 20})
	f := New(mem, true, false, DefaultHz)
	f.players = []*player.Player{player.New(mem, life)}

	require.True(t, f.Tick())
	mana, _ := player.New(mem, life).StatMana()
	require.EqualValues(t, 5, mana, "mana was held up although only life was frozen")
}

// Mana is held the same way, and separately: freezing one does not freeze the
// other.
func TestManaIsHeldOnItsOwn(t *testing.T) {
	mem := fake(t, []int32{100, 100, 100, 5, 20, 20})
	f := New(mem, false, true, DefaultHz)
	f.players = []*player.Player{player.New(mem, life)}

	require.True(t, f.Tick())
	mana, _ := player.New(mem, life).StatMana()
	require.EqualValues(t, 20, mana, "mana was not held up")

	life, _ := player.New(mem, life).StatLife()
	require.EqualValues(t, 100, life, "life moved although only mana was frozen")
	require.Zero(t, f.Saves, "a mana write was counted as a life restore")
}

// A run with nobody to freeze is a failure rather than a loop doing nothing.
func TestAFreezeWithNoPlayer(t *testing.T) {
	mem := memtest.New(base, 0x4000)
	f := New(mem, true, false, DefaultHz)
	require.ErrorIs(t, f.Run(t.Context(), 0, nil), ErrNoPlayer)
}

// The loop finds the player for itself, and says how many copies it is writing
// to.
func TestARunLocatesAndAnnounces(t *testing.T) {
	/*
		A player a scan can actually find, which the others do not need: the
		locator checks the name as well as the numbers, and a block with no name
		behind it is not one.
	*/
	mem := memtest.New(base, 0x4000)
	mem.PlantMonoString(base+0x40, "Nakama")
	mem.PlantPlayer(life, []int32{500, 500, 137, 200, 220, 220}, base+0x40)
	f := New(mem, true, false, 1000)

	announced := -1
	require.NoError(t, f.Run(t.Context(), 20*time.Millisecond,
		func(n int) { announced = n }))
	require.Positive(t, announced, "the run did not say what it was freezing")
	require.Equal(t, announced, f.Players(), "it announced a different number")
}

// And a cancelled run stops rather than waiting out its duration.
func TestACancelledRunStops(t *testing.T) {
	mem := fake(t, []int32{100, 100, 100, 20, 20, 20})
	f := New(mem, true, false, 1000)
	f.players = []*player.Player{player.New(mem, life)}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	require.NoError(t, f.Run(ctx, time.Hour, nil))
	require.Less(t, time.Since(started), time.Second, "it waited out the duration")
}

/*
A few failed passes in a row make the loop look for the player again.

One is not enough on purpose: a single pass can miss while the game is between
frames, and relocating on that costs a full memory scan every time it happens.
*/
func TestRepeatedFailuresRelocate(t *testing.T) {
	mem := fake(t, []int32{100, 100, 100, 20, 20, 20})
	f := New(mem, true, false, 10000)
	f.players = []*player.Player{player.New(mem, base+0x999999)}

	relocated := 0
	f.relocate = func() int {
		relocated++
		f.players = []*player.Player{player.New(mem, life)}
		return 1
	}
	require.NoError(t, f.Run(t.Context(), 50*time.Millisecond, nil))
	require.Positive(t, relocated, "dead addresses were never looked for again")
}
