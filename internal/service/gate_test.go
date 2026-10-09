package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/builds"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The build gate, and putting a saved configuration back into a fresh game.

These are the answers the panel gives after a game update, so being wrong is
loud in one direction and silent in the other: refusing a build that works
annoys, while accepting one whose offsets have moved writes numbers into the
wrong fields of a live save.
*/

// gateFixture is a game with a version, an anchor for one cheat, and a home to
// keep the records in.
func gateFixture(t *testing.T) (string, *execMem, *service.Service) {
	t.Helper()
	home := atHome(t)
	mem := plant()
	plantVersionInto(mem, version.KnownVersion)
	plantExtractorInto(mem)
	return home, mem, service.New(mem, -1)
}

/*
A game whose statics never change is a paused game.

Read from Main's statics rather than from the player: a player standing still
changes nothing, and reading them cost a wrong diagnosis once.
*/
func TestAPausedGameIsNotAdvancing(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	require.False(t, service.New(mem, -1).FramesAdvancing(time.Millisecond),
		"a game that changed nothing was reported as running")
}

// And one whose statics move is simulating.
func TestARunningGameIsAdvancing(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	ticking := &tickingMem{execMem: mem}

	require.True(t, service.New(ticking, -1).FramesAdvancing(time.Millisecond),
		"a game whose statics moved was reported as paused")
}

/*
tickingMem is a game whose statics change between reads, which is what a running
frame looks like from outside.
*/
type tickingMem struct {
	*execMem
	tick byte
}

func (m *tickingMem) Read(addr uint32, size int) []byte {
	if addr == staticAt {
		m.tick++
		m.PokeBytes(staticAt+0x300, []byte{m.tick})
	}
	return m.execMem.Read(addr, size)
}

/*
The runtime the game is running under is recorded with the decision.

The patches match code that runtime's JIT emitted, so a decision made under one
says nothing about another even for the same game build. Answered by the test
because which runtime is mapped is a fact about this machine, and on a
developer's box the answer is always none.
*/
func TestTheDecisionRecordsTheRuntime(t *testing.T) {
	home, mem, svc := gateFixture(t)
	service.WatchRuntime(t, "wine-mono-11.2.0")

	_, err := svc.AcceptBuild(builds.Accepted, nil)
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(memtest.ConfigUnder(home),
		"accepted-builds.json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "wine-mono-11.2.0",
		"the decision does not say which runtime it was made under")

	require.Equal(t, "wine-mono-11.2.0", svc.BuildCheck(patch.NewPatcher(mem, -1)).Runtime,
		"the check does not report the runtime it is looking at")
}

/*
The arena is allocated opportunistically, and only while the game is running.

Enabling a cheat from the panel unfocuses the game, which pauses it, which stops
the frames the allocation needs -- so waiting until the user asks is waiting for
a moment that does not come.
*/
func TestTheArenaIsNotAllocatedWhilePaused(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	p := patchFor(t, mem)

	started := time.Now()
	require.False(t, service.New(mem, -1).EnsureArena(p),
		"an arena was allocated in a game running no frames")
	/*
		And it gave up at once rather than waiting out the springboard.

		This runs on a poll while the panel is open, so a paused game must cost
		one look at the statics -- not three seconds of waiting for frames that
		are not coming.
	*/
	require.Less(t, time.Since(started), time.Second,
		"a paused game was waited on rather than skipped")
}

// An arena that is already there needs no frames at all.
func TestAnArenaAlreadyThereIsEnough(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	mem.PokeBytes(arenaAt+patch.ArenaMagicOff, patch.ArenaMagic)
	atHome(t)
	p := patch.NewPatcher(&arenaMem{execMem: mem}, -1)

	require.True(t, service.New(mem, -1).EnsureArena(p),
		"an arena that was already there was not recognised")
}

/*
An edit for an item nobody is carrying is ordinary, not a failure.

The player sold the sword. Reporting that as something going wrong would put a
warning on the screen every launch for as long as the edit is remembered.
*/
func TestRestoringAnEditForAnAbsentItem(t *testing.T) {
	atHome(t)
	mem := plant()
	svc := service.New(mem, -1)
	_, err := svc.RecordItemEdit(4242, map[string]float64{"damage": 77})
	require.NoError(t, err)

	got, err := svc.Restore(patch.NewPatcher(mem, -1))
	require.NoError(t, err)
	require.Equal(t, []int32{4242}, got.Absent, "an absent item was not reported as absent")
	require.Empty(t, got.Items, "something was written for an item nobody has")
	require.Empty(t, got.Skipped, "an absent item was reported as a failure")
}

// A cheat this version of the program has never heard of is skipped, not
// retried forever.
func TestRestoringAnUnknownCheat(t *testing.T) {
	atHome(t)
	mem := plant()
	require.NoError(t, profile.SetCheat("not_a_cheat_at_all", true, nil))

	got, err := service.New(mem, -1).Restore(patch.NewPatcher(mem, -1))
	require.NoError(t, err)
	require.Empty(t, got.Pending, "a name that is not a patch was queued for a retry")
	require.Empty(t, got.Cheats, "a name that is not a patch was applied")
	require.Equal(t, []string{"cheat:not_a_cheat_at_all"}, got.Skipped,
		"a cheat this version has never heard of went unreported")
}

/*
A cheat whose anchor is not there yet is pending rather than skipped.

The method may not be JIT-compiled at the moment the game starts, so the restore
runs again -- and reporting it as skipped would stop it ever being retried.
*/
func TestRestoringACheatThatCannotBeAppliedYet(t *testing.T) {
	atHome(t)
	mem := plant()
	require.NoError(t, profile.SetCheat("mining", true, nil))

	got, err := service.New(mem, -1).Restore(patch.NewPatcher(mem, -1))
	require.NoError(t, err)
	require.Equal(t, []string{"mining"}, got.Pending,
		"a cheat whose anchor is not there was not queued for a retry")
	require.Empty(t, got.Skipped, "it was written off instead")
}

/*
asGoShaped brings the Python's answer into the shape this side writes, in the
two ways they differ without disagreeing.

An absent string is None on that side and "" on this one, and the two spell the
same sentence with different punctuation -- the Python uses an em dash and
single quotes, this package writes ASCII. Neither is a difference of fact, and
normalising them is what lets the *reason* be compared at all: dropping it would
leave the one field a reader acts on unchecked.
*/
func asGoShaped(v any) any {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return strings.NewReplacer("\u2014", "--", "'", `"`).Replace(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, inner := range value {
			out[k] = asGoShaped(inner)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, inner := range value {
			out[i] = asGoShaped(inner)
		}
		return out
	default:
		return v
	}
}

/*
A runtime the version table has no numbers for is refused, forced or not.

Forcing exists for a build whose offsets might still fit. Numbers derived under
wine-mono are measured not to fit .NET Framework -- not one field offset is
shared -- so there is nothing for force to be right about (spec 052).
*/
func TestAnUnsupportedRuntimeIsRefusedEvenWhenForced(t *testing.T) {
	// netfx: an entry exists and reads the player, but may not write yet.
	// coreclr: no entry at all. Both refuse, for different stated reasons.
	for runtime, why := range map[string]string{
		"netfx-4.8.9345.0": "can only read so far",
		"coreclr-9.0.0":    "has no memory layout for that runtime",
	} {
		_, _, svc := gateFixture(t)
		service.WatchRuntime(t, runtime)
		for _, force := range []bool{false, true} {
			err := svc.RequireCompatible(force)
			require.Error(t, err, "%s force=%t", runtime, force)
			require.Contains(t, err.Error(), runtime)
			require.Contains(t, err.Error(), why)
			require.Contains(t, err.Error(), "nothing was changed")
		}
	}
}

// What Linux runs today still passes: any wine-mono, and a runtime that could
// not be detected, which is not evidence of anything.
func TestWineMonoAndAnUndetectedRuntimeStillPass(t *testing.T) {
	for _, runtime := range []string{"wine-mono-11.3.0", "wine-mono-10.4.1", ""} {
		_, _, svc := gateFixture(t)
		service.WatchRuntime(t, runtime)
		require.NoError(t, svc.RequireCompatible(false), "runtime %q", runtime)
	}
}

/*
A build this machine accepted is not recognised under a runtime with no numbers.

The decision is keyed by build, and the build matching is not the numbers
fitting. Before the version table, the Windows game read "known-good" on
exactly this basis. Recognised is checked true under wine-mono first, so the
false that follows is the runtime's doing and not a fixture that was never
recognised.
*/
func TestAnAcceptedBuildIsNotRecognisedUnderAnUnsupportedRuntime(t *testing.T) {
	_, mem, svc := gateFixture(t)
	service.WatchRuntime(t, "wine-mono-11.3.0")
	_, err := svc.AcceptBuild(builds.Accepted, nil)
	require.NoError(t, err)
	before := svc.BuildCheck(patch.NewPatcher(mem, -1))
	require.True(t, before.Recognised, "the accepted build is not recognised to begin with")
	require.NotEqual(t, "unsupported", before.Support)

	// The same game and the same recorded decision, under .NET Framework. A
	// fresh service, because a service keeps the runtime it first read.
	service.WatchRuntime(t, "netfx-4.8.9345.0")
	after := service.New(mem, -1).BuildCheck(patch.NewPatcher(mem, -1))
	require.True(t, after.ReadOnly, "the CLR entry reported as writable")
	require.False(t, before.ReadOnly)
	require.False(t, after.Recognised, "an accepted build is recognised under a runtime with no numbers")
	require.False(t, after.Known)
	require.Contains(t, after.Message, "netfx-4.8.9345.0")
}

// Nothing is recorded about a build running under a runtime with no numbers:
// a decision there would be a decision about nothing.
func TestNoDecisionIsRecordedUnderAnUnsupportedRuntime(t *testing.T) {
	home, _, svc := gateFixture(t)
	service.WatchRuntime(t, "netfx-4.8.9345.0")

	_, err := svc.AcceptBuild(builds.Accepted, nil)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(memtest.ConfigUnder(home), "accepted-builds.json"))
	require.True(t, os.IsNotExist(statErr), "a decision file was written anyway")
}

/*
Under the CLR entry the service finds a CLR player and reads life and mana, and
reads nothing it has no numbers for.

The game is planted the CLR way: the caps in CLR order and the name at the CLR
offset, which the mono locator does not find (internal/locate's own tests).
Inventory is left out of the snapshot rather than read through mono offsets,
and the inventory-count fallback for picking a copy is skipped for the same
reason.
*/
func TestTheCLREntryReadsTheCLRPlayerAndNothingMore(t *testing.T) {
	const base, size = 0x10000000, 0x10000
	mem := &execMem{memtest.New(base, size)}
	mem.PlantCLRString(base+0x40, "terrariabonker")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 420, 390, 200, 200, 220}, base+0x40)
	// Something readable where mono keeps the inventory pointer, as real memory
	// has: read through mono's numbers it looks like a full inventory. A
	// snapshot that read it would print items this player does not have.
	plantInventory(mem.FakeMem, base+0x800, base+0x4000, base+0x5000, liveItemsImage)
	svc := service.New(mem, -1).WithRuntime("netfx-4.8.9345.0")

	require.True(t, svc.CanRead(layout.ReadPlayer))
	require.False(t, svc.CanRead(layout.ReadInventory))
	require.False(t, svc.CanRead(layout.ReadLocalPlayer))

	snap := svc.Snapshot(true)
	require.Equal(t, 1, snap.Copies)
	require.NotNil(t, snap.Player)
	require.Equal(t, "terrariabonker", snap.Player.Name)
	require.EqualValues(t, 390, snap.Player.HP)
	require.EqualValues(t, 400, snap.Player.MaxHP, "the permanent cap, which the CLR stores first")
	require.Empty(t, snap.Inventory, "the inventory was read with another runtime's numbers")

	// The same memory under the mono entry finds no one: the fail-safe.
	require.Zero(t, service.New(mem, -1).WithRuntime("wine-mono-11.3.0").Snapshot(true).Copies)
}
