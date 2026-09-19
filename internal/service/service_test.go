package service_test

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

	"github.com/ushineko/terrariabonker/internal/service"
)

/*
The two services find the same player, believe the same copy and write to all of
them.

Which copy is live is the question this layer answers, and getting it wrong is
silent: the numbers change in memory and the game reads a different object. So
the reads are compared against the Python over an image with two copies in it,
and every write is compared as the whole buffer, which shows both that the live
copy was hit and that the inert one was too.
*/

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

/*
realHome is the maintainer's home directory as it was before any test moved it.

Tests that touch the patch record or the profile point HOME at a scratch
directory. The Python child must not inherit that: its interpreter works out
where the user's packages are at startup, from HOME, and a moved one makes numpy
unimportable -- which the helper below reports as "the Python is not importable"
and skips.

That is worse than a failure, because a skipped comparison looks like a passing
one. Several of the fishing tests were skipping in silence before this.
*/
var realHome = os.Getenv("HOME")

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// newService is a service over a freshly planted game.
func newService(t *testing.T) (*execMem, *service.Service) {
	t.Helper()
	mem := plant()
	return mem, service.New(mem, -1)
}

/*
Both find both copies, and both believe the same one.

The believed copy is the one behind get_LocalPlayer. The fixture is built so
that neither fallback can reach it -- both copies are hurt, so the activity guess
refuses, and the snapshot is no poorer, so the inventory guess picks that one --
which is what makes this fail if the resolver stops being consulted.
*/
func TestFindingTheLivePlayerMatchesThePython(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
print(json.dumps({
    "copies": [b.life_addr for b in svc.players()],
    "live": svc.live_block().life_addr,
}))`, &want)

	_, svc := newService(t)
	blocks, err := svc.Players()
	require.NoError(t, err)

	var addrs []uint32
	for _, b := range blocks {
		addrs = append(addrs, b.LifeAddr)
	}
	require.Equal(t, want["copies"], asJSON(t, addrs), "a different set of copies")

	live, err := svc.LiveBlock()
	require.NoError(t, err)
	require.Equal(t, want["live"], asJSON(t, live.LifeAddr), "a different copy is believed")
	require.Equal(t, liveIs, live.LifeAddr, "and it is not the one behind get_LocalPlayer")
}

// The player and the inventory that get reported are the live copy's.
func TestSnapshotMatchesThePython(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
snap = svc.snapshot()
p = snap.player
print(json.dumps({
    "copies": snap.copies,
    "player": {"name": p.name, "hp": p.hp, "max_hp": p.max_hp,
               "mana": p.mana, "max_mana": p.max_mana},
    "inventory": [vars(s) for s in snap.inventory],
    "bare": [vars(s) for s in svc.inventory()],
    "empty": len(svc.snapshot(with_inventory=False).inventory),
}))`, &want)

	_, svc := newService(t)
	snap := svc.Snapshot(true)
	require.Equal(t, want["copies"], asJSON(t, snap.Copies), "a different number of copies")
	require.Equal(t, want["player"], asJSON(t, snap.Player), "a different player")
	require.Equal(t, want["inventory"], asJSON(t, snap.Inventory), "a different inventory")

	slots, err := svc.Inventory()
	require.NoError(t, err)
	require.Equal(t, want["bare"], asJSON(t, slots), "a different inventory on its own")
	require.Equal(t, want["empty"], asJSON(t, len(svc.Snapshot(false).Inventory)),
		"asking for no inventory returned one")
}

// Every write reaches every copy, and lands on the same bytes.
func TestWritesReachEveryCopy(t *testing.T) {
	cases := []struct {
		name   string
		python string
		run    func(*service.Service) error
	}{
		{"hp", "svc.set_hp(42)", func(s *service.Service) error { return s.SetHP(42) }},
		// "max" fills each copy to *that copy's* cap, which is why it is a
		// separate call rather than a number read from one of them.
		{"hp max", `svc.set_hp("max")`, func(s *service.Service) error { return s.SetHPMax() }},
		{"mana", "svc.set_mana(7)", func(s *service.Service) error { return s.SetMana(7) }},
		{"mana max", `svc.set_mana("max")`, func(s *service.Service) error { return s.SetManaMax() }},
		{"max hp", "svc.set_max_hp(600)", func(s *service.Service) error { return s.SetMaxHP(600) }},
		{"max mana", "svc.set_max_mana(400)", func(s *service.Service) error { return s.SetMaxMana(400) }},
		{"stack", "svc.set_stack(1, 99)", func(s *service.Service) error { return s.SetStack(1, 99) }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				Buf string `json:"buf"`
			}
			askPython(t, preamble()+c.python+`
print(json.dumps({"buf": mem.buf.hex()}))`, &want)

			mem, svc := newService(t)
			require.NoError(t, c.run(svc))
			require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
		})
	}
}

/*
A sweep writes to every copy and reports the live copy's slots.

The inert copy here holds more pickaxes than the live player does. Reporting
whichever copy was written last would report those, and the number goes straight
to the user.
*/
func TestASweepReportsTheLiveCopy(t *testing.T) {
	for _, c := range []struct {
		name   string
		python string
		live   []int // the live copy's slots, from the fixture
		run    func(*service.Service) ([]int, error)
	}{
		// The live player carries pickaxes in slots 0 and 4; the snapshot
		// carries them in 0 and 3.
		{"fast mining", "svc.fast_mining()", []int{0, 4}, func(s *service.Service) ([]int, error) {
			return s.FastMining(8, 13, 200)
		}},
		// The live player's items are in 0 to 4; the snapshot's are in 0 to 3
		// and 7.
		{"long reach", "svc.long_reach()", []int{0, 1, 2, 3, 4}, func(s *service.Service) ([]int, error) {
			return s.LongReach(20)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				Hit []int  `json:"hit"`
				Buf string `json:"buf"`
			}
			askPython(t, preamble()+`
hit = `+c.python+`
print(json.dumps({"hit": hit, "buf": mem.buf.hex()}))`, &want)

			mem, svc := newService(t)
			hit, err := c.run(svc)
			require.NoError(t, err)
			require.Equal(t, want.Hit, hit, "different slots were reported")
			require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
			// Both copies are written, so agreeing with the Python does not
			// show which copy was *reported*: the two would agree on the wrong
			// one. The fixture gives them different inventories, and these are
			// the live copy's slots.
			require.Equal(t, c.live, hit, "the slots reported are not the live copy's")
		})
	}
}

/*
With no player loaded, a read is an ordinary empty answer and a write says so.

The window asks for a snapshot on a timer, and a game sitting at the main menu
is not an error to put on screen.
*/
func TestNoPlayerLoaded(t *testing.T) {
	var want map[string]any
	askPython(t, `
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import locate as L
from terrariabonker.service import Service, ServiceError
mem = FakeMem(0x10000000, 0x4000)
L._exec_regions = lambda m: []
svc = Service(mem)
snap = svc.snapshot()
try:
    svc.set_hp(1)
    wrote = True
except ServiceError:
    wrote = False
print(json.dumps({"copies": snap.copies, "player": snap.player,
                  "inventory": snap.inventory, "wrote": wrote}))`, &want)
	require.Nil(t, want["player"], "the Python found a player in empty memory")

	mem := plantNothing()
	svc := service.New(mem, -1)
	snap := svc.Snapshot(true)
	require.Equal(t, want["copies"], asJSON(t, snap.Copies), "a different number of copies")
	require.Nil(t, snap.Player, "a player was found in empty memory")
	require.Equal(t, want["inventory"], asJSON(t, snap.Inventory), "an inventory was found")

	err := svc.SetHP(1)
	require.Error(t, err, "a write went through with no player")
	require.True(t, service.IsNoPlayer(err), "and it was not reported as no player")
}

/*
The located copies are kept, and dropped when the player moves.

Re-locating costs a full heap scan, so the worker keeps the result; but a world
reload puts the player in a new object, and a cache kept across that leaves
every write landing on a dead one. The stale entry still reads back as the same
named player, so only the live copy no longer being among them catches it.
*/
func TestTheCacheIsDroppedWhenThePlayerMoves(t *testing.T) {
	mem, svc := newService(t)

	first, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, first, 3)

	// Nothing has changed: the same addresses come back.
	again, err := svc.Players()
	require.NoError(t, err)
	require.Equal(t, first, again, "the cache was thrown away for no reason")

	// The live player moves: get_LocalPlayer now points somewhere else, and a
	// player is planted there.
	moved := uint32(base + 0xC000)
	mem.PlantMonoString(liveName, "Nakama")
	mem.PlantPlayer(moved+0x738, liveBlock, liveName)
	mem.PokeBytes(playerArray+layoutArrData+myPlayer*4, u32(moved))

	after, err := svc.Players()
	require.NoError(t, err)
	require.NotEqual(t, first, after, "the cache survived the player moving")

	live, err := svc.LiveBlock()
	require.NoError(t, err)
	require.Equal(t, moved+0x738, live.LifeAddr, "the believed copy did not move with it")
}

/*
An anchor that has gone is not a reason to trust the cache.

A copy that has been left behind still reads back as the same named player, so
without ground truth there is nothing that can confirm the cached addresses. The
safe answer is to scan again, and the unsafe one is invisible: writes keep
landing on an object the game stopped reading.
*/
func TestLosingTheAnchorForcesARescan(t *testing.T) {
	mem, svc := newService(t)
	first, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, first, 3)

	// get_LocalPlayer is gone, as it would be after a game update, and a third
	// copy has appeared, which is what a rescan would see and a kept cache
	// would not.
	mem.PokeBytes(code, make([]byte, 0x40))
	mem.PlantPlayer(base+0xC738, liveBlock, liveName)

	after, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, after, 4, "the cache was kept without ground truth to confirm it")
}

/*
A cached address that stops reading as the same player is dropped.

This is the managed heap collecting: the object moves, and what is left at the
address is whatever was allocated there next.
*/
func TestACopyThatStopsReadingForcesARescan(t *testing.T) {
	mem, svc := newService(t)
	first, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, first, 3)

	// The snapshot copy is overwritten by something that is not a player.
	mem.PokeI32(snapLife, 0)
	mem.PokeI32(snapLife-4, 0)
	mem.PokeI32(snapLife-8, 0)

	after, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, after, 2, "an address that stopped being a player was kept")
	require.Equal(t, liveIs, after[0].LifeAddr, "and the wrong one survived")
}

/*
A cached copy that is now somebody else is dropped, not reported.

The object at a cached address can be collected and the space handed to another
Player, which still passes every check a life and mana block gets. The name is
what tells them apart, and reporting the cached one means reporting a player who
is not there.
*/
func TestACopyThatBecomesSomebodyElseForcesARescan(t *testing.T) {
	mem, svc := newService(t)
	first, err := svc.Players()
	require.NoError(t, err)
	require.Equal(t, "Nakama", first[0].Name)

	// Same block, same address, different player.
	mem.PlantMonoString(snapName, "Someone")

	after, err := svc.Players()
	require.NoError(t, err)
	require.Len(t, after, 3, "the copies changed when only a name did")
	require.Equal(t, "Someone", after[0].Name, "a stale name was reported from the cache")
}
