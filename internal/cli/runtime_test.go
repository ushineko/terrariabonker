package cli_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/service"
)

// runUnder runs one argv over a planted game, with the service told which
// runtime is executing it.
func runUnder(t *testing.T, runtime string, mem *execMem, argv ...string) (int, string, string) {
	t.Helper()
	memtest.IsolateHome(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Attach: func() (*cli.Game, error) {
			return &cli.Game{
				Svc:     service.New(mem, -1).WithRuntime(runtime),
				Patcher: patch.NewPatcher(mem, -1), PID: os.Getpid(),
			}, nil
		},
	})
	code := app.Execute(context.Background(), argv, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// clrGame is a game with one player and two items planted the CLR way.
func clrGame() *execMem {
	const base, size = 0x10000000, 0x20000
	mem := &execMem{FakeMem: memtest.New(base, size)}
	mem.PlantCLRString(base+0x40, "terrariabonker")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 420, 390, 200, 200, 220}, base+0x40)
	mem.PlantCLRInventory(base+0x800, base+0xC000, base+0xD000, []memtest.CLRItem{
		{Slot: 0, Type: 757, Stack: 1, Damage: 85, AutoReuse: true},
		{Slot: 3, Type: 2, Stack: 250},
	})
	return mem
}

/*
Under .NET Framework, status and inventory read the player and their items with
the CLR's numbers, and reads the entry cannot do are refused by name rather than
run with another runtime's numbers.

The item catalog reads the CLR's item and NPC templates (ReadContent), so it runs
and emits its JSON; what it reads is internal/service's to test (spec 052). A
write is refused by the build gate, which internal/service tests.
*/
func TestUnderTheCLRThePlayerAndInventoryAreRead(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"

	code, out, errOut := runUnder(t, runtime, clrGame(), "status")
	require.Zero(t, code, errOut)
	require.Contains(t, out, `"terrariabonker": HP 390/420  Mana 200/200`)
	require.Contains(t, out, "slot  0: type=757   stack=1 dmg=85 auto")
	require.Contains(t, out, "slot  3: type=2     stack=250")

	code, out, errOut = runUnder(t, runtime, clrGame(), "inventory")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "type=757")

	code, out, errOut = runUnder(t, runtime, clrGame(), "compendium")
	require.Zero(t, code, errOut)
	require.Contains(t, out, `"items"`)
}

/*
Under .NET Framework, the stat writes run and land in the CLR's fields.

set-max-hp writes both caps, and the CLR stores them where clrfields says:
statLifeMax at statLife-8, statLifeMax2 at statLife-4. The debug write pokes an
address the caller names with no number of this program's, so it runs under any
entry that may write at all, and is refused where none may.
*/
func TestUnderTheCLRTheStatWritesAndTheDebugWriteRun(t *testing.T) {
	const runtime, life = "netfx-4.8.9345.0", 0x10000800
	mem := clrGame()

	code, _, errOut := runUnder(t, runtime, mem, "set-hp", "111")
	require.Zero(t, code, errOut)
	got, _ := mem.ReadI32(life)
	require.EqualValues(t, 111, got)

	code, _, errOut = runUnder(t, runtime, mem, "set-max-hp", "460")
	require.Zero(t, code, errOut)
	for _, off := range []uint32{8, 4} {
		v, _ := mem.ReadI32(life - off)
		require.EqualValues(t, 460, v, "the cap at statLife-%d", off)
	}

	code, _, errOut = runUnder(t, runtime, mem, "write", "10000900", "5")
	require.Zero(t, code, errOut)
	got, _ = mem.ReadI32(0x10000900)
	require.EqualValues(t, 5, got, "the debug write did not land where it was pointed")

	const unknown = "coreclr-8.0.0"
	before := mem.Hex()
	code, _, errOut = runUnder(t, unknown, mem, "write", "10000900", "9")
	require.NotZero(t, code, "a write ran under a runtime no entry covers")
	require.Contains(t, errOut, unknown)
	require.Equal(t, before, mem.Hex(), "the refused write changed something")
}

/*
Under .NET Framework godmode holds the live character's life at the cap in
effect, through the CLR's player numbers.

clrGame's player has 390 of 420. The freeze finds them by the CLR's name offset:
the mono locator, which the freeze used to ask, finds nobody in this memory.
*/
func TestUnderTheCLRGodmodeHoldsLife(t *testing.T) {
	const runtime, life = "netfx-4.8.9345.0", 0x10000800
	mem := clrGame()

	code, out, errOut := runUnder(t, runtime, mem, "godmode", "--seconds", "0.05")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "on 1 copy(ies)")
	got, _ := mem.ReadI32(life)
	require.EqualValues(t, 420, got, "life was not held at the cap in effect")
}

/*
With two characters in memory and no telling which is played, a freeze holds
neither and says why.

The selection screen keeps every character as a Player object. clrGame has no
Main.player to settle which is live, so the freeze is refused as every other
write is, where it used to hold every copy it found.
*/
func TestAFreezeWithTwoCharactersAndNoGroundTruthIsRefused(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrGame()
	mem.PlantCLRString(0x10000080, "someone else")
	mem.PlantCLRPlayer(0x10001800, []int32{300, 300, 120, 100, 100, 100}, 0x10000080)
	before := mem.Hex()

	code, _, errOut := runUnder(t, runtime, mem, "freeze", "--godmode", "--mana", "--seconds", "0.05")
	require.NotZero(t, code, "a freeze ran with nobody to say which character is played")
	require.Contains(t, errOut, "cannot be told")
	require.Equal(t, before, mem.Hex(), "the refused freeze wrote something")
}

/*
Under .NET Framework the window's vein watcher runs behind the ore extractor's
own write feature, as `extract` does, where it was refused with every unported
write.

clrGame has no world, so extract-tick gets as far as looking for the tile map and
says what it could not find -- the command running; the refusal names the runtime
and never looks. extract-stop has no watcher to drop and says so.
*/
func TestUnderTheCLRTheVeinWatcherRuns(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrGame()
	before := mem.Hex()

	code, _, errOut := runUnder(t, runtime, mem, "extract-tick", "--budget", "0")
	require.NotZero(t, code)
	require.NotEmpty(t, errOut)
	require.NotContains(t, errOut, runtime, "extract-tick was refused for the runtime")

	code, out, errOut := runUnder(t, runtime, mem, "extract-stop", "--json")
	require.Zero(t, code, errOut)
	require.Contains(t, out, `"stopped":false`)

	require.Equal(t, before, mem.Hex(), "something was written")
}

/*
Under .NET Framework the fishing gear and selling each run behind a write feature
of their own, where they were refused with every other unported write.

fishing tops a bait stack back up: stack at the CLR's 0x064, on an item whose
bait power (0x04C) says it is bait. sell is asked for a dry run with nothing on
the list and a Piggy Bank carried, so it runs to the end and writes nothing.
*/
func TestUnderTheCLRFishingGearAndSellingRun(t *testing.T) {
	const runtime, items = "netfx-4.8.9345.0", 0x1000D000
	mem := clrGame()
	mem.PlantCLRInventory(0x10000800, 0x1000C000, items, []memtest.CLRItem{
		{Slot: 5, Type: 2676, Stack: 4},
		{Slot: 6, Type: 87, Stack: 1},
	})
	mem.PokeBytes(items+5*memtest.CLRItemSize+0x04C, []byte{50})

	code, out, errOut := runUnder(t, runtime, mem, "fishing", "--no-kit", "--keep", "30")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "topped up slot 5 4->30")
	stack, _ := mem.ReadI32(items + 5*memtest.CLRItemSize + 0x064)
	require.EqualValues(t, 30, stack)

	before := mem.Hex()
	code, out, errOut = runUnder(t, runtime, mem, "sell", "--dry-run")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "nothing to sell")
	code, _, errOut = runUnder(t, runtime, mem, "sell-tick", "--dry-run")
	require.Zero(t, code, errOut)
	require.Equal(t, before, mem.Hex(), "a dry run wrote something")
}

/*
Under .NET Framework spawning and the projectile editor each run behind a write
feature of their own, where they were refused with every other unported write.

clrGame is a player with no world around them, so there is no Main.npc and no
Main.projectile: each command gets as far as looking for its array and says it is
not there, which is the command running -- the refusal names the runtime and
never looks. projectile-of reads Item.shoot at the CLR's 0x0BC, from the one item
of that type in memory.
*/
func TestUnderTheCLRSpawningAndTheProjectileEditorRun(t *testing.T) {
	const runtime, items = "netfx-4.8.9345.0", 0x1000D000
	mem := clrGame()
	mem.PokeI32(items+0x0BC, 132)
	before := mem.Hex()

	code, _, errOut := runUnder(t, runtime, mem, "spawn-npc", "1")
	require.NotZero(t, code)
	require.Contains(t, errOut, "could not find Main.npc")
	require.NotContains(t, errOut, runtime, "spawn-npc was refused for the runtime")

	code, _, errOut = runUnder(t, runtime, mem, "projectile-tick", "--set", "132:scale=2", "--budget", "0")
	require.NotZero(t, code)
	require.Contains(t, errOut, "could not locate Main.projectile")
	require.NotContains(t, errOut, runtime, "projectile-tick was refused for the runtime")

	code, out, errOut := runUnder(t, runtime, mem, "projectile-stop")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "stopped")

	code, out, errOut = runUnder(t, runtime, mem, "projectile-of", "757")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "item 757 shoots projectile 132")

	require.Equal(t, before, mem.Hex(), "something was written")
}

/*
Under .NET Framework extract-recipes runs as a read of its own, where it sat
behind the every-write gate.

clrGame has no Main.player slot, so the command gets as far as looking for the
statics and says they are not there -- before it would write the book into the
working directory. Under a runtime no entry covers it is refused by name.
*/
func TestUnderTheCLRExtractRecipesRuns(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	t.Chdir(t.TempDir())

	code, _, errOut := runUnder(t, runtime, clrGame(), "extract-recipes")
	require.NotZero(t, code)
	require.Contains(t, errOut, "Main's statics")
	require.NotContains(t, errOut, runtime, "extract-recipes was refused for the runtime")

	const unknown = "coreclr-8.0.0"
	code, _, errOut = runUnder(t, unknown, clrGame(), "extract-recipes")
	require.NotZero(t, code)
	require.Contains(t, errOut, unknown)

	_, err := os.Stat("data")
	require.ErrorIs(t, err, os.ErrNotExist, "a book was written")
}
