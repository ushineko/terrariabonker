package tiles_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
A planted world, and what is read out of it.

The tile reader is where a silent failure lives: the active bit's offset is
measured rather than derived, and a wrong one lands on padding that reads as a
constant zero, so every tile looks mined. A self-consistent fixture cannot catch
that -- which is why the real check runs against a live world, and why what is
checked here is the reading, tile for tile, against the plan that was planted.

The flood orders and the whitelists below were agreed with the implementation
this was ported from, while both existed.
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
		at := objects + uint32(idx)*record //nolint:gosec // a planted address
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

/*
Every coordinate reads back as what was planted there, including the ones the
type alone cannot separate.

A dirt block and open air are both id 0 and differ only in the active bit; a
mined ore reads back as id 0 because clearing zeroes both; and a coordinate with
no object at all is different again from one with an inactive object.
*/
func TestReadingTilesIsWhatWasPlanted(t *testing.T) {
	read := tm(t)

	for x := int32(-1); x <= worldW; x++ {
		for y := int32(-1); y <= worldH; y++ {
			at := [2]int32{x, y}
			want, inside := plan[at]

			id, haveID := read.TypeAt(x, y)
			active, haveActive := read.ActiveAt(x, y)
			solid, haveSolid := read.SolidTypeAt(x, y)

			if !inside || want.noObject {
				require.Falsef(t, haveID, "an id was read at %v", at)
				require.Falsef(t, haveActive, "an active bit was read at %v", at)
				require.Falsef(t, haveSolid, "a solid id was read at %v", at)
				continue
			}
			require.Truef(t, haveID, "no id at %v", at)
			require.Equalf(t, want.id, id, "a different id at %v", at)
			require.Truef(t, haveActive, "no active bit at %v", at)
			require.Equalf(t, want.active, active, "a different active bit at %v", at)

			require.Equalf(t, want.active, haveSolid,
				"%v is solid on one reading and not the other", at)
			if want.active {
				require.Equalf(t, want.id, solid, "a different solid id at %v", at)
			}
		}
	}
}

/*
A column comes back as the tiles in it, including where it runs past the world.

A nil in the list is a coordinate with no object, which is not the same as a
tile whose id is zero -- and the column is what the falling-ore search walks.
*/
func TestColumn(t *testing.T) {
	for _, c := range []struct {
		x, y0, y1 int32
		want      []int // -1 for a coordinate with no object
	}{
		{2, 0, worldH, []int{0, 0, 0, 0, 0, 0, 1, 7, 7, 7, 1, 1}},
		{2, 5, 9, []int{0, 1, 7, 7}},
		{6, 8, 12, []int{1, 1, -1, 1}},
		{0, -5, 3, []int{0, 0, 0}},
		{99, 0, 3, nil},
		{2, 5, 5, nil},
	} {
		t.Run(fmt.Sprintf("%d_%d_%d", c.x, c.y0, c.y1), func(t *testing.T) {
			got := tm(t).Column(c.x, c.y0, c.y1)
			require.Len(t, got, len(c.want), "a different number of tiles came back")
			for i, want := range c.want {
				if want < 0 {
					require.Falsef(t, got[i].Present, "tile %d has an object", i)
					continue
				}
				require.Truef(t, got[i].Present, "tile %d has no object", i)
				require.EqualValuesf(t, want, got[i].Type, "tile %d is a different id", i)
			}
		})
	}
}

// A whole-world search finds the coordinates in order, column by column.
func TestFindType(t *testing.T) {
	for _, c := range []struct {
		want  uint16
		limit int
		found [][2]int32
	}{
		{want: 7, found: [][2]int32{{1, 6}, {2, 7}, {2, 8}, {2, 9}, {3, 7}, {3, 8},
			{3, 9}, {4, 6}, {5, 9}}},
		{want: 6, found: [][2]int32{{4, 7}, {4, 8}}},
		// Stopped early, which is what the caller's budget does.
		{want: 7, limit: 2, found: [][2]int32{{1, 6}, {2, 7}}},
		{want: 999, found: nil},
	} {
		t.Run(fmt.Sprintf("id%d_limit%d", c.want, c.limit), func(t *testing.T) {
			got := tm(t).FindType(c.want, c.limit)
			require.Len(t, got, len(c.found), "a different number of tiles was found")
			for i, want := range c.found {
				require.Equalf(t, want[0], got[i].X, "hit %d is at a different x", i)
				require.Equalf(t, want[1], got[i].Y, "hit %d is at a different y", i)
			}
		})
	}
}

/*
A flood takes one vein, in a fixed order.

The order matters and is not incidental: the caller queues these for the game to
mine, a batch at a time, so a change that kept the set and reordered it would
mine different tiles in the first batch.
*/
func TestFlood(t *testing.T) {
	for _, c := range []struct {
		name     string
		x, y     int32
		limit    int
		diagonal bool
		want     [][2]int32
	}{
		{
			name: "into the copper", x: 2, y: 7, limit: tiles.DefaultLimit, diagonal: true,
			want: [][2]int32{{2, 7}, {3, 7}, {2, 8}, {3, 8}, {1, 6}, {3, 9}, {4, 6}},
		},
		{
			name: "into the iron beside it", x: 4, y: 7, limit: tiles.DefaultLimit,
			diagonal: true, want: [][2]int32{{4, 7}, {4, 8}},
		},
		{
			name: "stopped early", x: 2, y: 7, limit: 2, diagonal: true,
			want: [][2]int32{{2, 7}, {3, 7}},
		},
		{
			name: "stopped mid-spread", x: 2, y: 7, limit: 3, diagonal: true,
			want: [][2]int32{{2, 7}, {3, 7}, {2, 8}},
		},
		{
			name: "without the diagonals", x: 2, y: 7, limit: tiles.DefaultLimit,
			want: [][2]int32{{2, 7}, {3, 7}, {2, 8}, {3, 8}, {3, 9}},
		},
		{name: "empty sky", x: 0, y: 0, limit: tiles.DefaultLimit, diagonal: true},
		{
			name: "stone, which is not on the list", x: 0, y: 6,
			limit: tiles.DefaultLimit, diagonal: true,
		},
		{
			name: "an ore's id where no tile is", x: 5, y: 9,
			limit: tiles.DefaultLimit, diagonal: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := tiles.Flood(tm(t), c.x, c.y, tiles.Whitelist(false), c.limit, c.diagonal)
			require.Len(t, got, len(c.want), "a different number of tiles was taken")
			for i, want := range c.want {
				require.Equalf(t, want[0], got[i].X, "tile %d is at a different x", i)
				require.Equalf(t, want[1], got[i].Y, "tile %d is at a different y", i)
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

/*
The whitelist is the ores, and gems are opt-in.

Frozen because it is a hand-written list of tile ids: a number added to it is a
tile the extractor will mine, and one removed is ore somebody expected to go and
did not.
*/
func TestWhitelist(t *testing.T) {
	ores := []int{6, 7, 8, 9, 22, 37, 56, 58, 107, 108, 111, 123, 166, 167, 168,
		169, 204, 211, 221, 222, 223, 224, 404, 407, 408}
	withGems := []int{6, 7, 8, 9, 22, 37, 56, 58, 63, 64, 65, 66, 67, 68, 107, 108,
		111, 123, 166, 167, 168, 169, 204, 211, 221, 222, 223, 224, 404, 407, 408}

	for _, c := range []struct {
		gems bool
		want []int
	}{{gems: false, want: ores}, {gems: true, want: withGems}} {
		got := tiles.Whitelist(c.gems)
		ids := make([]int, 0, len(got))
		for id := range got {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		require.Equalf(t, c.want, ids, "the whitelist differs with gems=%v", c.gems)
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
