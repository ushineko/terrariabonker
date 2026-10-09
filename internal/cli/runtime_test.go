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

// clrGame is a game with one player planted the CLR way.
func clrGame() *execMem {
	const base, size = 0x10000000, 0x4000
	mem := &execMem{memtest.New(base, size)}
	mem.PlantCLRString(base+0x40, "terrariabonker")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 420, 390, 200, 200, 220}, base+0x40)
	return mem
}

/*
Under .NET Framework, status reads the player, and reads the entry cannot do are
refused by name rather than run with another runtime's numbers.

status shows life and mana and no inventory; inventory and the item catalog say
which runtime and what cannot be read. A write is refused by the build gate,
which internal/service tests.
*/
func TestUnderTheCLROnlyThePlayerIsRead(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"

	code, out, errOut := runUnder(t, runtime, clrGame(), "status")
	require.Zero(t, code, errOut)
	require.Contains(t, out, `"terrariabonker": HP 390/400  Mana 200/200`)
	require.NotContains(t, out, "slot", "status printed inventory read with mono offsets")

	for _, argv := range [][]string{{"inventory"}, {"compendium"}} {
		code, _, errOut := runUnder(t, runtime, clrGame(), argv...)
		require.NotZero(t, code, "%v ran under the CLR entry", argv)
		require.Contains(t, errOut, runtime, "%v", argv)
	}
}
