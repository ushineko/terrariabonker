package tiles_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
A planted world, read by both implementations.

The tile reader is where a silent failure lives: the active bit's offset is
measured rather than derived, and a wrong one lands on padding that reads as a
constant zero, so every tile looks mined. Nothing about a self-consistent fixture
catches that -- which is why the real check runs against a live world and why what
is compared here is the reading, tile for tile.
*/

const (
	base       = 0x10000000
	size       = 0x40000
	staticBase = base + 0x100

	tileBuf = base + 0x1000 // the buffer object
	/*
		Its {width, originX, height, originY}, placed well clear of the pointer
		array that follows the buffer object.

		It sat 0x100 past the buffer at first, and the array ran straight over it
		and clobbered the stride. Both implementations planted it the same way and
		so agreed about the resulting nonsense -- which is what the assertion below
		about a vein being four tiles is for, and why it is written against the
		fixture rather than against the other implementation.
	*/
	tileBounds = base + 0x4000
	entries    = tileBuf + 0x10
	objects    = base + 0x8000 // where the tile objects go

	worldW, worldH = 8, 12
	// The pool lays tile objects out 24 bytes apart down a column, which is what
	// the fast path in the search depends on.
	record = 24
)

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

/*
world is what is planted at each coordinate: the tile id, whether a tile is
really there, and whether there is an object at all.

It carries the cases the reader has to separate. A dirt block and open air are
both id 0 and differ only in the active bit. A mined ore reads back as id 0
because clearing zeroes both. And a coordinate with no object at all is different
again from one with an inactive object.
*/
type cell struct {
	id       uint16
	active   bool
	noObject bool
}

// plan is the planted world, column-major as the game stores it.
var plan = func() map[[2]int32]cell {
	out := map[[2]int32]cell{}
	for x := int32(0); x < worldW; x++ {
		for y := int32(0); y < worldH; y++ {
			switch {
			case y < 3: // sky: objects exist, nothing is there
				out[[2]int32{x, y}] = cell{id: 0, active: false}
			case y < 6: // dirt: id 0 and active, which the type alone cannot tell from air
				out[[2]int32{x, y}] = cell{id: 0, active: true}
			default: // stone
				out[[2]int32{x, y}] = cell{id: 1, active: true}
			}
		}
	}
	/*
		A copper vein, and an iron one touching it: a flood from either must take
		one ore and not both.

		The copper is not a square. (1,6) touches (2,7) diagonally and nothing
		else, so it joins only when the diagonal steps are taken.

		And it branches twice: (3,7) leads to (4,6) while (3,8) leads to (3,9), so
		which of those is reached first depends on which end of the queue the
		walk takes from. With one branch -- or none -- depth-first and
		breadth-first produced the same order and that was untestable.
	*/
	for _, p := range [][2]int32{{2, 7}, {3, 7}, {2, 8}, {3, 8}, {3, 9}, {1, 6}, {4, 6}} {
		out[[2]int32{p[0], p[1]}] = cell{id: 7, active: true}
	}
	for _, p := range [][2]int32{{4, 7}, {4, 8}} {
		out[[2]int32{p[0], p[1]}] = cell{id: 6, active: true}
	}
	/*
		Two tiles that are not there but whose entries still carry an ore's id.

		That is what the reader documents -- an id is whatever the entry still
		says, and only the active bit says whether a tile is really there. The
		first sits beside the copper, so a flood that spread on the id alone
		would swallow it; the second is somewhere on its own, so a flood *started*
		on it would take a vein out of empty space.
	*/
	out[[2]int32{2, 9}] = cell{id: 7, active: false}
	out[[2]int32{5, 9}] = cell{id: 7, active: false}
	// And a coordinate the buffer has no object for at all.
	out[[2]int32{6, 10}] = cell{noObject: true}
	return out
}()

// plant writes the world into a Go fake.
func plant() *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(staticBase+0x99C, u32(tileBuf)) // Main.tile
	mem.PokeI32(staticBase+0x5A4, worldW)         // maxTilesX
	mem.PokeI32(staticBase+0x5A4+4, worldH)       // maxTilesY
	mem.PokeBytes(tileBuf+0x08, u32(tileBounds))  // the bounds object
	mem.PokeI32(tileBounds+0x04, 0)               // originX
	mem.PokeI32(tileBounds+0x08, worldH)          // the stride, which is the height
	mem.PokeI32(tileBounds+0x0C, 0)               // originY

	for _, key := range sortedKeys(plan) {
		x, y := key[0], key[1]
		c := plan[key]
		idx := worldH*x + y
		if c.noObject {
			continue
		}
		// Laid out contiguously down each column, as the pool does.
		at := uint32(objects + uint32(idx)*record) //nolint:gosec // a planted address
		mem.PokeBytes(entries+uint32(idx)*4, u32(at))
		mem.PokeBytes(at+0x08, u16(c.id))
		var header uint16
		if c.active {
			header = 0x20
		}
		mem.PokeBytes(at+0x0E, u16(header))
	}
	return mem
}

// pyPlant is the same world as Python source, written with the Python's own
// offsets.
func pyPlant() string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import layout, tiles
mem = FakeMem(%d, %d)
mem.poke_bytes(%d + layout.MAIN_TILE_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + layout.MAIN_MAX_TILES_OFF, %d)
mem.poke_i32(%d + layout.MAIN_MAX_TILES_OFF + 4, %d)
mem.poke_bytes(%d + 0x08, struct.pack("<I", %d))
mem.poke_i32(%d + 0x04, 0)
mem.poke_i32(%d + 0x08, %d)
mem.poke_i32(%d + 0x0C, 0)
`, base, size, staticBase, tileBuf, staticBase, worldW, staticBase, worldH,
		tileBuf, tileBounds, tileBounds, tileBounds, worldH, tileBounds)

	for _, key := range sortedKeys(plan) {
		x, y := key[0], key[1]
		c := plan[key]
		if c.noObject {
			continue
		}
		idx := worldH*x + y
		at := objects + idx*record
		header := 0
		if c.active {
			header = 0x20
		}
		fmt.Fprintf(&b, "mem.poke_bytes(%d + 4 * %d, struct.pack(\"<I\", %d))\n",
			entries, idx, at)
		fmt.Fprintf(&b, "mem.poke_bytes(%d + 0x08, struct.pack(\"<H\", %d))\n", at, c.id)
		fmt.Fprintf(&b, "mem.poke_bytes(%d + 0x0E, struct.pack(\"<H\", %d))\n", at, header)
	}
	fmt.Fprintf(&b, "tm = tiles.TileMap(mem, %d)\n", staticBase)
	return b.String()
}

// Every coordinate reads the same, including the ones the type alone cannot
// separate.
func TestReadingTilesMatchesThePython(t *testing.T) {
	var want map[string][]any
	askPython(t, pyPlant()+fmt.Sprintf(`
out = {}
for x in range(-1, %d + 1):
    for y in range(-1, %d + 1):
        out["%%d,%%d" %% (x, y)] = [tm.type_at(x, y), tm.active_at(x, y),
                                    tm.solid_type_at(x, y)]
print(json.dumps(out))`, worldW, worldH), &want)

	tm, err := tiles.New(plant(), staticBase)
	require.NoError(t, err)

	for key, w := range want {
		var x, y int32
		_, err := fmt.Sscanf(key, "%d,%d", &x, &y)
		require.NoError(t, err)

		typ, haveType := tm.TypeAt(x, y)
		require.Equalf(t, w[0], nilOr(typ, haveType), "the id at %s differs", key)

		active, haveActive := tm.ActiveAt(x, y)
		require.Equalf(t, w[1], nilOrBool(active, haveActive), "active at %s differs", key)

		solid, haveSolid := tm.SolidTypeAt(x, y)
		require.Equalf(t, w[2], nilOr(solid, haveSolid), "the solid id at %s differs", key)
	}
}

// A column comes back the same, including where it runs past the world.
func TestColumnMatchesThePython(t *testing.T) {
	for _, c := range [][3]int32{{2, 0, worldH}, {2, 5, 9}, {6, 8, 12}, {0, -5, 3}, {99, 0, 3}, {2, 5, 5}} {
		t.Run(fmt.Sprintf("%d_%d_%d", c[0], c[1], c[2]), func(t *testing.T) {
			var want []any
			askPython(t, pyPlant()+fmt.Sprintf("print(json.dumps(tm.column(%d, %d, %d)))",
				c[0], c[1], c[2]), &want)

			got := tm(t).Column(c[0], c[1], c[2])
			require.Len(t, got, len(want), "a different number of tiles came back")
			for i, w := range want {
				require.Equalf(t, w, nilOr(got[i].Type, got[i].Present),
					"tile %d of the column differs", i)
			}
		})
	}
}

// A whole-world search finds the same coordinates, in the same order.
func TestFindTypeMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		want  uint16
		limit int
	}{{7, 0}, {6, 0}, {1, 0}, {0, 0}, {7, 2}, {999, 0}} {
		t.Run(fmt.Sprintf("id%d_limit%d", c.want, c.limit), func(t *testing.T) {
			var found [][]int32
			askPython(t, pyPlant()+fmt.Sprintf("print(json.dumps(tm.find_type(%d, %d)))",
				c.want, c.limit), &found)

			got := tm(t).FindType(c.want, c.limit)
			require.Len(t, got, len(found), "a different number of tiles was found")
			for i, w := range found {
				require.Equalf(t, w[0], got[i].X, "hit %d is at a different x", i)
				require.Equalf(t, w[1], got[i].Y, "hit %d is at a different y", i)
			}
		})
	}
}

/*
A flood takes one vein, in the same order.

The order matters and is not incidental: the caller queues these for the game to
mine, a batch at a time, so two implementations that agreed on the set and
disagreed on the order would mine different tiles in the first batch.
*/
func TestFloodMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		x, y     int32
		gems     bool
		limit    int
		diagonal bool
	}{
		{2, 7, false, tiles.DefaultLimit, true},  // into the copper
		{4, 7, false, tiles.DefaultLimit, true},  // into the iron beside it
		{2, 7, false, 2, true},                   // and stopped early
		{2, 7, false, 3, true},                   // stopped mid-spread
		{2, 7, false, tiles.DefaultLimit, false}, // without the diagonals
		{0, 0, false, tiles.DefaultLimit, true},  // empty sky
		{0, 6, false, tiles.DefaultLimit, true},  // stone, which is not on the list
		{5, 9, false, tiles.DefaultLimit, true},  // an ore's id where no tile is
	} {
		t.Run(fmt.Sprintf("%d_%d_limit%d_diag%v", c.x, c.y, c.limit, c.diagonal), func(t *testing.T) {
			var want [][]int32
			askPython(t, pyPlant()+fmt.Sprintf(`
print(json.dumps(tiles.flood(tm, %d, %d, tiles.whitelist(%s), limit=%d, diagonal=%s)))`,
				c.x, c.y, pyBool(c.gems), c.limit, pyBool(c.diagonal)), &want)

			got := tiles.Flood(tm(t), c.x, c.y, tiles.Whitelist(c.gems), c.limit, c.diagonal)
			require.Len(t, got, len(want), "a different number of tiles was taken")
			for i, w := range want {
				require.Equalf(t, w[0], got[i].X, "tile %d is at a different x", i)
				require.Equalf(t, w[1], got[i].Y, "tile %d is at a different y", i)
			}
		})
	}
}

// A vein is one ore: a flood into copper touching iron takes only the copper.
func TestAVeinIsOneOre(t *testing.T) {
	got := tiles.Flood(tm(t), 2, 7, tiles.Whitelist(false), tiles.DefaultLimit, true)
	require.Len(t, got, 7, "the flood did not take the whole copper vein")
	for _, p := range got {
		id, ok := tm(t).SolidTypeAt(p.X, p.Y)
		require.Truef(t, ok, "the flood took a tile that is not there at %v", p)
		require.Equalf(t, uint16(7), id, "the flood took a tile of another ore at %v", p)
	}

	// Without the diagonals the tile that only touches the vein corner-to-corner
	// is not part of it.
	straight := tiles.Flood(tm(t), 2, 7, tiles.Whitelist(false), tiles.DefaultLimit, false)
	require.Len(t, straight, 5, "the diagonal steps changed nothing")

	// The iron beside it is untouched, and a flood started in the iron does not
	// wander into the copper.
	iron := tiles.Flood(tm(t), 4, 7, tiles.Whitelist(false), tiles.DefaultLimit, true)
	require.Len(t, iron, 2, "the iron vein is not the size it was planted")
}

/*
A tile that is not there cannot start a vein, whatever its entry still says.

The entry keeps an id after the tile is gone, so a flood that trusted the id
would mine a vein out of empty space -- and the coordinates go straight to the
game to be mined.
*/
func TestAFloodWillNotStartOnEmptySpace(t *testing.T) {
	require.Empty(t, tiles.Flood(tm(t), 5, 9, tiles.Whitelist(false), tiles.DefaultLimit, true),
		"a vein was started on a tile that is not there")

	// Nor spread into one: (2,9) carries copper's id below the vein and is not
	// there either.
	for _, p := range tiles.Flood(tm(t), 2, 7, tiles.Whitelist(false), tiles.DefaultLimit, true) {
		require.NotEqual(t, tiles.Point{X: 2, Y: 9}, p,
			"the flood spread into a tile that is not there")
	}
}

// The whitelist is the same set, and gems are opt-in.
func TestWhitelistMatchesThePython(t *testing.T) {
	for _, gems := range []bool{false, true} {
		var want []int
		askPython(t, `
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import tiles
print(json.dumps(sorted(tiles.whitelist(`+pyBool(gems)+`))))`, &want)

		got := tiles.Whitelist(gems)
		ids := make([]int, 0, len(got))
		for id := range got {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		require.Equalf(t, want, ids, "the whitelist differs with gems=%v", gems)
	}
}

// A world that is not loaded is said to be, rather than read as an empty one.
func TestAWorldThatIsNotLoaded(t *testing.T) {
	_, err := tiles.New(memtest.New(base, size), staticBase)
	require.Error(t, err, "a world was read where none is loaded")
	require.Contains(t, err.Error(), "is a world loaded")
}

func tm(t *testing.T) *tiles.TileMap {
	t.Helper()
	got, err := tiles.New(plant(), staticBase)
	require.NoError(t, err)
	return got
}

/*
A whole-world search reads each contiguous run once, not each tile.

The tile objects are pool-allocated a fixed distance apart down a column, and
reading a whole run and striding the ids out of it is the difference between
0.15 s and 13.2 s against a real world. That is an observation about the
allocator rather than a guaranteed layout, so the search checks the spacing as it
goes and falls back to a read per tile -- which is why getting it wrong is
invisible to every other test here: the answers stay right and only the cost
moves.

So the cost is what is measured.
*/
func TestTheSearchReadsRunsRatherThanTiles(t *testing.T) {
	counted := &counter{FakeMem: plant()}
	tm, err := tiles.New(counted, staticBase)
	require.NoError(t, err)

	counted.reads = 0
	tm.FindType(7, 0)

	/*
		One read of the pointers per column, plus one per contiguous run. The
		planted world lays every column out in one run, so eight columns cost
		sixteen reads. A read per tile would be eight columns of twelve.
	*/
	require.LessOrEqualf(t, counted.reads, 2*worldW+2,
		"the search read %d times for %d tiles: the run fast path is not being used",
		counted.reads, worldW*worldH)
}

// counter is a fake that records how many times it was read.
type counter struct {
	*memtest.FakeMem
	reads int
}

func (c *counter) Read(addr uint32, size int) []byte {
	c.reads++
	return c.FakeMem.Read(addr, size)
}
