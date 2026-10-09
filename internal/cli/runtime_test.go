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
	mem := &execMem{memtest.New(base, size)}
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

The item catalog walks many structures (NPCs, templates) the CLR entry has no
readers for yet, so it is still refused. A write is refused by the build gate,
which internal/service tests.
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

	code, _, errOut = runUnder(t, runtime, clrGame(), "compendium")
	require.NotZero(t, code, "the catalog ran under the CLR entry")
	require.Contains(t, errOut, runtime)
}

/*
Under .NET Framework, the stat writes run and land in the CLR's fields; a write
whose path still uses mono's numbers is refused.

set-max-hp writes both caps, and the CLR stores them where clrfields says:
statLifeMax at statLife-8, statLifeMax2 at statLife-4. set-stack is an inventory
write, which the CLR entry does not allow yet.
*/
func TestUnderTheCLRTheStatWritesRunAndOthersAreRefused(t *testing.T) {
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

	before := mem.Hex()
	code, _, errOut = runUnder(t, runtime, mem, "set-stack", "0", "99")
	require.NotZero(t, code, "an inventory write ran under the CLR entry")
	require.Contains(t, errOut, runtime)
	require.Equal(t, before, mem.Hex(), "the refused write changed something")
}
