package service_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
