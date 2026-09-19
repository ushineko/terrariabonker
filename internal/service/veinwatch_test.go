package service_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Watching for a tile being broken, and taking the rest of its vein.

This is the shape people expect from a vein miner: break one ore by hand and the
connected run goes with it. What makes it hard is that it has to notice in time
-- a full rescan of the window costs a third of a second and with fast mining the
player breaks several blocks inside that -- so the ore map is kept between rounds
and only re-taken when it goes stale.
*/

// watchingGame is a game that really mines, with the player standing by a vein.
func watchingGame(t *testing.T) (*miningMem, *patch.Patcher, *service.Service) {
	t.Helper()
	game, p := miningGame(t, false)
	plantVeinInto(game.execMem)
	plantPositionInto(game.execMem, veinX*16, veinY*16)
	return game, p, service.New(game, -1)
}

/*
Breaking one tile of a vein takes the rest of it.

The first round learns where the ore is and takes nothing; the tile then goes,
as the player mining it would take it; the next round notices and the vein
follows.
*/
func TestBreakingATileTakesTheVein(t *testing.T) {
	game, p, svc := watchingGame(t)
	w, err := svc.NewVeinWatch(p, false, 0, 0, time.Second)
	require.NoError(t, err)
	defer w.Close()

	first, err := w.Round()
	require.NoError(t, err)
	require.Empty(t, first, "a vein was taken before anything was broken")

	plantTile(game.execMem, veinX, veinY, 0, false) // the player breaks one

	got, err := w.Round()
	require.NoError(t, err)
	require.Len(t, got, 1, "breaking a tile did not take its vein")
	require.Equal(t, [2]int32{veinX, veinY}, *got[0].TriggeredBy,
		"the report names a different tile as the trigger")
	require.Equal(t, 3, got[0].Mined, "the rest of the vein was not taken")

	tm, err := svc.TileMap()
	require.NoError(t, err)
	for _, q := range [][2]int32{{veinX + 1, veinY}, {veinX, veinY + 1}, {veinX + 1, veinY + 1}} {
		_, there := tm.SolidTypeAt(q[0], q[1])
		require.Falsef(t, there, "%v was left standing", q)
	}
	// And the iron beside the vein is not part of it.
	_, iron := tm.SolidTypeAt(veinX+2, veinY)
	require.True(t, iron, "the iron beside the vein went with it")
}

/*
Breaking something that is not ore sets nothing off.

The watcher is looking at tiles it already knows are whitelisted, so this is
about what it put in that list rather than about what it does with it.
*/
func TestBreakingPlainStoneTakesNothing(t *testing.T) {
	game, p, svc := watchingGame(t)
	w, err := svc.NewVeinWatch(p, false, 0, 0, time.Second)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Round()
	require.NoError(t, err)

	plantTile(game.execMem, veinX-3, veinY, 0, false) // stone, not ore
	got, err := w.Round()
	require.NoError(t, err)
	require.Empty(t, got, "breaking plain stone set the extractor off")
}

/*
A tile with nothing of its own kind beside it is not a vein.

Breaking a single ore is just mining a block; queueing for it would arm the stub
for a flood of one and disarm again every time somebody picked up a stray.
*/
func TestBreakingALoneTileTakesNothing(t *testing.T) {
	game, p, svc := watchingGame(t)
	w, err := svc.NewVeinWatch(p, false, 0, 0, time.Second)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Round()
	require.NoError(t, err)

	plantTile(game.execMem, veinX, veinY+30, 0, false) // the lone deposit below
	got, err := w.Round()
	require.NoError(t, err)
	require.Empty(t, got, "a lone tile was treated as a vein")
}

/*
The window follows the player rather than where they started.

Ore outside it is invisible, and a watcher that scanned once and never again
stops triggering the moment somebody walks away -- which reads as the cheat
having switched itself off.
*/
func TestTheWindowFollowsThePlayer(t *testing.T) {
	game, p, svc := watchingGame(t)
	// A window too small to see the vein from where the player will be standing.
	w, err := svc.NewVeinWatch(p, false, 0, 4, time.Second)
	require.NoError(t, err)
	defer w.Close()

	plantPositionInto(game.execMem, (veinX+40)*16, veinY*16)
	_, err = w.Round()
	require.NoError(t, err)

	// They walk back to the vein and break a tile.
	plantPositionInto(game.execMem, veinX*16, veinY*16)
	_, err = w.Round()
	require.NoError(t, err)
	plantTile(game.execMem, veinX, veinY, 0, false)

	got, err := w.Round()
	require.NoError(t, err)
	require.Len(t, got, 1, "the window did not move with the player")
}

/*
The window is as wide as the player can actually mine.

With tool reach on they break tiles seventy-five away, and a window sized for
vanilla silently stops triggering the moment they mine at range.
*/
func TestTheWindowCoversTheToolReach(t *testing.T) {
	game, _, svc := watchingGame(t)
	/*
		The reach is read from the record rather than applied, because this
		fixture has no anchor for that cheat -- and the radius comes from the
		recorded value either way, which is what is being checked.
	*/
	require.NoError(t, os.WriteFile(patchRecord(t),
		[]byte(`{"pid": -1, "values": {"tool_reach": 100}}`), 0o600))
	p := patch.NewPatcher(game, -1)

	w, err := svc.NewVeinWatch(p, false, 0, 0, time.Second)
	require.NoError(t, err)
	defer w.Close()

	/*
		The player stands a hundred and ten tiles away: past the vanilla window,
		past the reach itself, and inside the margin the window adds on top. All
		three numbers matter and this is the distance that tells them apart.
	*/
	plantPositionInto(game.execMem, (veinX+110)*16, veinY*16)
	_, err = w.Round()
	require.NoError(t, err)

	plantTile(game.execMem, veinX, veinY, 0, false)
	got, err := w.Round()
	require.NoError(t, err)
	require.Len(t, got, 1, "the window was not widened to the tool reach")
}

// Without the extractor applied there is nothing to watch with, and it says so
// rather than watching and queueing tiles nothing will mine.
func TestWatchingWithoutTheCheat(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantVeinInto(mem)
	p := patchFor(t, mem)

	_, err := service.New(mem, -1).NewVeinWatch(p, false, 0, 0, time.Second)
	require.ErrorContains(t, err, "not enabled")
}

/*
A tick runs several rounds within its budget and hands results back promptly.

The window drives this across a process boundary, where a round trip costs more
than a round does -- so batching keeps detection tight without a timer firing at
fifty hertz through the privileged worker.
*/
func TestATickRunsSeveralRoundsAndStopsOnAResult(t *testing.T) {
	game, p, svc := watchingGame(t)
	defer svc.WatchStop()

	got, err := svc.WatchTick(p, false, 0, time.Second, 50*time.Millisecond)
	require.NoError(t, err)
	require.Greater(t, got.Rounds, 1, "a tick ran a single round and went home")
	require.Empty(t, got.Events, "a vein was taken before anything was broken")

	plantTile(game.execMem, veinX, veinY, 0, false)
	got, err = svc.WatchTick(p, false, 0, time.Second, time.Second)
	require.NoError(t, err)
	require.Len(t, got.Events, 1, "the broken tile was not noticed")
	require.Equal(t, 3, got.Mined, "a different amount was mined")
	require.Equal(t, 1, got.Rounds,
		"the tick kept going after it had something to report")
}

// Stopping drops the watcher, and says whether there was one.
func TestStoppingTheWatcher(t *testing.T) {
	_, p, svc := watchingGame(t)
	require.False(t, svc.WatchStop(), "a watcher that was never started was stopped")

	_, err := svc.WatchTick(p, false, 0, time.Second, 10*time.Millisecond)
	require.NoError(t, err)
	require.True(t, svc.WatchStop(), "a running watcher was not stopped")
	require.False(t, svc.WatchStop(), "it was stopped twice")
}

// patchRecord is where the patch record goes under the scratch home the mining
// fixture set up.
func patchRecord(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	dir := filepath.Join(home, ".config", "terrariabonker")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return filepath.Join(dir, "patches.json")
}

/*
breakingMem is a game where the player breaks a tile between rounds.

A round reads the player's position before it does anything else, so counting
those reads is counting rounds -- which is what lets a blocking loop be watched
without a second goroutine poking at the same memory while it runs.
*/
type breakingMem struct {
	*miningMem
	rounds int
	at     [2]int32
}

func (m *breakingMem) Read(addr uint32, size int) []byte {
	if addr == uint32(liveLife)-0x738+0x0C && size == 8 {
		if m.rounds++; m.rounds == 2 {
			plantTile(m.execMem, m.at[0], m.at[1], 0, false)
		}
	}
	return m.miningMem.Read(addr, size)
}

/*
The blocking loop runs its rounds and reports what they took.

What the command line runs; the window drives the tick instead, and the two
share the watcher underneath.
*/
func TestTheBlockingWatchRunsItsRounds(t *testing.T) {
	game, p, _ := watchingGame(t)
	breaking := &breakingMem{miningMem: game, at: [2]int32{veinX, veinY}}
	svc := service.New(breaking, -1)

	var seen []service.Extracted
	got, err := svc.WatchVeins(t.Context(), p, false, 0, 0, time.Second, 3,
		func(e service.Extracted) { seen = append(seen, e) })
	require.NoError(t, err)
	require.Equal(t, 3, got.Rounds, "a different number of rounds was run")
	require.Equal(t, 3, got.Mined, "the vein was not taken")
	require.Len(t, seen, 1, "the caller was told about a different number of veins")
	require.Equal(t, seen, got.Events, "what was reported is not what came back")
}

/*
Closing the watcher disarms the extractor.

A queue left armed is re-mined on every frame, so a watcher that stopped without
disarming would leave the stub working on whatever was last queued -- and there
is nothing on screen to say that is happening.
*/
func TestClosingTheWatcherDisarmsTheExtractor(t *testing.T) {
	_, p, svc := watchingGame(t)
	w, err := svc.NewVeinWatch(p, false, 0, 0, time.Second)
	require.NoError(t, err)

	require.Positive(t, p.OreQueue().Arm([]patch.Tile{{X: veinX, Y: veinY}}),
		"nothing could be queued, so disarming proves nothing")
	require.True(t, p.OreQueue().Armed(), "the queue did not take the tile")

	w.Close()
	require.False(t, p.OreQueue().Armed(), "the queue was left armed")
}
