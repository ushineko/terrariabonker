package cli_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
A game to run commands against, and a way to run them without root.

The command tree is what turns an argv into one service call and a line of
text, and both halves are worth checking: an operation wired to the wrong flag
does the wrong thing quietly, and a line of output is the only thing a person
ever sees.

Nothing here tests what the operations *do* -- that is the service package's
business, against the same planted game.
*/

const (
	base = 0x10000000
	size = 0x60000

	code           = base + 0x1000
	staticAt       = base + 0x10000
	playerStatic   = staticAt + layout.MainPlayerOff
	myPlayerStatic = playerStatic + 4
	playerArray    = base + 0x200
	myPlayer       = 0

	liveObj  = base + 0x8000
	liveLife = liveObj + 0x738 // locate.StatLifeFromObj
	liveName = base + 0x80

	liveArr   = base + 0x20000
	liveItems = base + 0x24000
	itemSpace = 0x400

	itemVTable = 0xDEADBEEF
)

// liveBlock is the player's life and mana, as a scan recognises them.
var liveBlock = []int32{500, 500, 137, 200, 220, 220}

// execMem is a fake whose whole buffer is code as well, so the resolver has
// somewhere to look.
type execMem struct{ *memtest.FakeMem }

func (m *execMem) ExePath() string { return "" }

func (m *execMem) AllRegions() []proc.Region {
	return []proc.Region{{
		Start: base, End: base + size,
		Readable: true, Writable: true, Executable: true,
	}}
}

// plantGame is a game with one player in it, carrying two items.
func plantGame() *execMem {
	mem := memtest.New(base, size)
	mem.Exec = []proc.Region{{Start: base, End: base + size, Executable: true}}

	asm := append([]byte{0x8B, 0x05}, u32(playerStatic)...)
	asm = append(asm, 0x8B, 0x0D)
	asm = append(asm, u32(myPlayerStatic)...)
	asm = append(asm, localPlayerTail...)
	mem.PokeBytes(code, asm)
	mem.PokeBytes(playerStatic, u32(playerArray))
	mem.PokeI32(myPlayerStatic, myPlayer)
	mem.PokeBytes(playerArray+layout.ArrDataOff+myPlayer*4, u32(liveObj))

	mem.PlantMonoString(liveName, "Nakama")
	mem.PlantPlayer(liveLife, liveBlock, liveName)

	mem.PokeBytes(uint32(int(liveLife)+layout.InventoryPtrOff), u32(liveArr)) //nolint:gosec // a delta
	/*
		Every slot holds an item object, as the game's do: an empty slot is an
		item of type zero rather than a null, which is what makes "list the empty
		ones too" a thing there is anything to list.
	*/
	for i := range layout.InventorySlots {
		addr := uint32(liveItems + i*itemSpace)                         //nolint:gosec // a slot index
		mem.PokeBytes(liveArr+layout.ArrDataOff+uint32(i)*4, u32(addr)) //nolint:gosec // a slot index
		mem.PokeBytes(addr, u32(itemVTable))
	}
	for _, it := range []struct {
		slot            int
		itemType, stack int32
		pick, damage    int32
	}{
		{slot: 0, itemType: 3509, stack: 1, pick: 210, damage: 35},
		{slot: 4, itemType: 9, stack: 99},
	} {
		addr := uint32(liveItems + it.slot*itemSpace)                         //nolint:gosec // a slot index
		mem.PokeBytes(liveArr+layout.ArrDataOff+uint32(it.slot)*4, u32(addr)) //nolint:gosec // a slot index
		mem.PokeBytes(addr, u32(itemVTable))
		mem.PokeI32(addr+uint32(layout.ItemType), it.itemType) //nolint:gosec // a field offset
		mem.PokeI32(addr+uint32(layout.ItemStack), it.stack)   //nolint:gosec // a field offset
		mem.PokeI32(addr+uint32(layout.ItemPick), it.pick)     //nolint:gosec // a field offset
		mem.PokeI32(addr+uint32(layout.ItemDamage), it.damage) //nolint:gosec // a field offset
		mem.PokeI32(addr+uint32(layout.ItemUseTime), 20)       //nolint:gosec // a field offset
	}
	return &execMem{mem}
}

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// localPlayerTail is the pattern the resolver looks for, pinned against the
// Python by the locate package's own tests.
var localPlayerTail = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}

/*
run builds a command tree over the planted game and runs one argv.

Elevation is answered rather than performed: it re-execs the process under
sudo, which a test cannot do and must not want to.
*/
func run(t *testing.T, mem *execMem, argv ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Attach: func() (*cli.Game, error) {
			return &cli.Game{
				/*
					The test's own process stands in for the game.

					The worker drops its warm attachment when the pid is gone,
					which is how a game restart is noticed -- so a fixture with a
					pid that never existed re-attaches on every request and the
					whole point of the worker goes untested.
				*/
				Svc: service.New(mem, -1), Patcher: patch.NewPatcher(mem, -1),
				PID: os.Getpid(),
			}, nil
		},
	})
	code := app.Execute(context.Background(), argv, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// ranOK runs an argv and insists it succeeded, returning what it printed.
func ranOK(t *testing.T, mem *execMem, argv ...string) string {
	t.Helper()
	code, stdout, stderr := run(t, mem, argv...)
	require.Equalf(t, cli.ExitOK, code, "%v failed: %s", argv, stderr)
	return stdout
}

/*
plantVersionInto writes a version string where the game keeps it, twice.

Twice is what makes it the running version rather than a constant: the detector
wants agreement, because the runtime's own version string is in the same process
and can outnumber the game's while it is still loading.
*/
func plantVersionInto(mem *execMem, v string) {
	mem.PokeBytes(versionAt, make([]byte, 0x100))
	if v == "" {
		return
	}
	for i := range uint32(2) {
		mem.PokeBytes(versionAt+i*0x80, utf16le("v"+v))
	}
}

// versionAt is somewhere clear of everything else the fixture plants.
const versionAt = base + 0x600

// utf16le is a string as the game stores it.
func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

/*
plantWorldInto loads a world, which is what makes `status` say which one.

The panel re-applies the profile on a world switch and not only on a new pid, so
the world in a status reply is load-bearing rather than decoration.
*/
func plantWorldInto(mem *execMem, name string) {
	const (
		tileBufAt    = staticAt + 0x1000
		tileBoundsAt = staticAt + 0x2000
		worldNameAt  = staticAt + 0x3000
		worldWidth   = 40
		worldHeight  = 60
	)
	mem.PokeBytes(staticAt+layout.MainTileOff, u32(tileBufAt))
	mem.PokeI32(staticAt+layout.MainMaxTilesOff, worldWidth)
	mem.PokeI32(staticAt+layout.MainMaxTilesOff+4, worldHeight)
	mem.PokeBytes(tileBufAt+0x08, u32(tileBoundsAt))
	mem.PokeI32(tileBoundsAt+0x04, 0)
	mem.PokeI32(tileBoundsAt+0x08, worldHeight)
	mem.PokeI32(tileBoundsAt+0x0C, 0)

	mem.PlantMonoString(worldNameAt, name)
	mem.PokeBytes(staticAt+layout.MainWorldNameOff, u32(worldNameAt))
}
