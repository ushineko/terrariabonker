/*
Package tiles is a read-only view of the world's tiles.

The interesting parts are two measurements and one refusal. The active bit's
offset is measured rather than derived, because adding up the declared field
widths gives the wrong answer and the failure is silent. The tile objects' spacing
is an observation about the allocator, so the fast path checks it as it goes and
falls back rather than trusting it. And the whitelist is a list of ores rather
than "anything that looks minable", because a flood fill with a loose rule eats a
region.

Ported from terrariabonker/tiles.py (spec 051, step 5).
*/
package tiles

import (
	"encoding/binary"
	"fmt"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Where things are inside the tile buffer and inside one tile.

The active bit's offset is **measured**, not derived. Adding up the declared field
widths -- type, wall, liquid -- puts the header at 0x0C, and that is wrong: mono
lays a class out however it likes and leaves two bytes there. Reading it at 0x0C
returns a constant zero, which makes every tile look mined. CheckActiveOffset
exists because that failure is silent and a self-consistent test fixture will not
catch it.
*/
const (
	boundsOff     = 0x08 // to {width, originX, height, originY}
	entriesOff    = layout.ArrDataOff
	tileTypeOff   = 0x08 // ushort, within a tile object
	tileHeaderOff = 0x0E // sTileHeader, ushort
	activeBit     = 0x20 // active() is (sTileHeader & 32) == 32
)

/*
tileRecord is how far apart the tile objects sit.

They are pool-allocated and run contiguously down a column: a 1200-tile column
measured 1,197 deltas of exactly this and only three breaks, with no gaps.
Reading a whole run at once and striding the type out of it is what makes a
full-world search affordable -- 0.15 s against 5,040,000 tiles, where one read per
tile extrapolated to 13.2 s.

This is an **allocator observation, not a guaranteed layout**, so the search
verifies the stride as it goes and falls back to per-tile reads for any run that
does not hold. A different allocation pattern costs speed and never correctness.
*/
const tileRecord = 24

/*
Ores is the vanilla ore tile ids.

Every id here is checked against the game's own table by a test, which reads them
out of the bundled data. The list started as a hand-written one and the check
found two omissions, Luminite and Fossil Ore, which is exactly why it is checked
rather than trusted.
*/
var Ores = map[int]string{
	7: "Copper", 6: "Iron", 9: "Silver", 8: "Gold",
	166: "Tin", 167: "Lead", 168: "Tungsten", 169: "Platinum",
	22: "Demonite", 204: "Crimtane", 37: "Meteorite", 58: "Hellstone",
	107: "Cobalt", 221: "Palladium", 108: "Mythril", 222: "Orichalcum",
	111: "Adamantite", 223: "Titanium", 211: "Chlorophyte",
	408: "LunarOre", 407: "FossilOre",
}

/*
Extractables are not ores, but they are what an ore extractor is for.

Swept up by default, since leaving silt behind while mining a vein through it is
not what anyone means by the feature.
*/
var Extractables = map[int]string{123: "Silt", 224: "Slush", 404: "DesertFossil"}

// Gems are neither ore nor feedstock, and are opt-in: some players are
// deliberately leaving them in place.
var Gems = map[int]string{63: "Sapphire", 64: "Ruby", 65: "Emerald", 66: "Topaz",
	67: "Amethyst", 68: "Diamond"}

/*
WorldFormed is worth taking and floods like an ore, but is not one.

Kept out of Ores so that name stays literally true. Nothing generates obsidian:
it appears wherever water has run into lava, so a "vein" of it is whatever shape
the two fluids left behind -- and the lava that made it is usually still next to
it. Swept by default all the same.
*/
var WorldFormed = map[int]string{56: "Obsidian"}

// Whitelist is the tile ids a vein miner may take.
func Whitelist(gems bool) map[int]bool {
	out := map[int]bool{}
	for _, set := range []map[int]string{Ores, Extractables, WorldFormed} {
		for id := range set {
			out[id] = true
		}
	}
	if gems {
		for id := range Gems {
			out[id] = true
		}
	}
	return out
}

// DefaultLimit caps a flood fill: a vein of contiguous ore is small, and the cap
// keeps a mistake from walking the whole world.
const DefaultLimit = 400

// Mem is the memory the world is read out of.
type Mem interface {
	Read(addr uint32, size int) []byte
	ReadU32(addr uint32) (uint32, bool)
	ReadI32(addr uint32) (int32, bool)
}

// TileMap is a read-only view of the world's tiles.
type TileMap struct {
	Mem     Mem
	Buf     uint32
	Stride  int32
	OriginX int32
	OriginY int32
	MaxX    int32
	MaxY    int32
}

// New is a view of the world the given Main static block describes.
func New(mem Mem, staticBase uint32) (*TileMap, error) {
	buf, _ := mem.ReadU32(staticBase + layout.MainTileOff)
	var bounds uint32
	if buf != 0 {
		bounds, _ = mem.ReadU32(buf + boundsOff)
	}
	if bounds == 0 {
		return nil, fmt.Errorf("cannot read Main.tile -- is a world loaded?")
	}
	tm := &TileMap{Mem: mem, Buf: buf}
	tm.Stride, _ = mem.ReadI32(bounds + 0x08) // the buffer's height, which is the stride
	tm.OriginX, _ = mem.ReadI32(bounds + 0x04)
	tm.OriginY, _ = mem.ReadI32(bounds + 0x0C)
	tm.MaxX, _ = mem.ReadI32(staticBase + layout.MainMaxTilesOff)
	tm.MaxY, _ = mem.ReadI32(staticBase + layout.MainMaxTilesOff + 4)
	if tm.Stride == 0 || tm.MaxX == 0 || tm.MaxY == 0 {
		return nil, fmt.Errorf("world dimensions unreadable")
	}
	return tm, nil
}

// InWorld reports whether a coordinate is inside the world at all.
func (t *TileMap) InWorld(x, y int32) bool {
	return x >= 0 && x < t.MaxX && y >= 0 && y < t.MaxY
}

// entry is the tile object at a coordinate, or zero when there is none.
func (t *TileMap) entry(x, y int32) uint32 {
	idx := t.Stride*(x-t.OriginX) + (y - t.OriginY)
	p, _ := t.Mem.ReadU32(t.Buf + entriesOff + 4*uint32(idx)) //nolint:gosec // an index into the buffer
	return p
}

/*
TypeAt is the tile id at a coordinate, and whether there is a tile object there.

Id 0 is dirt *and* empty space, so this cannot tell a dirt block from air. No ore
id is 0, so a whitelist search does not care; SolidTypeAt is what separates the
two when it matters.
*/
func (t *TileMap) TypeAt(x, y int32) (uint16, bool) {
	if !t.InWorld(x, y) {
		return 0, false
	}
	p := t.entry(x, y)
	if p == 0 {
		return 0, false
	}
	raw := t.Mem.Read(p+tileTypeOff, 2)
	if len(raw) < 2 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(raw), true
}

/*
ActiveAt reports whether there is actually a tile at a coordinate.

This is *not* what separates a mined tile from a standing one: mining goes
through a clear that zeroes the type and the header together, so a mined tile
reads back as id 0 anyway. It is what separates a **dirt block**, id 0 and
active, from **air**, id 0 and inactive -- the one distinction the type cannot
make.
*/
func (t *TileMap) ActiveAt(x, y int32) (bool, bool) {
	if !t.InWorld(x, y) {
		return false, false
	}
	p := t.entry(x, y)
	if p == 0 {
		return false, false
	}
	raw := t.Mem.Read(p+tileHeaderOff, 2)
	if len(raw) < 2 {
		return false, false
	}
	return binary.LittleEndian.Uint16(raw)&activeBit != 0, true
}

// SolidTypeAt is the tile id where a tile is really there, and nothing for empty
// space whatever stale id the entry still carries.
func (t *TileMap) SolidTypeAt(x, y int32) (uint16, bool) {
	if active, ok := t.ActiveAt(x, y); !ok || !active {
		return 0, false
	}
	return t.TypeAt(x, y)
}

/*
Column is the tile ids down one column, taking the pointers in a single read.

The index is column-major, so a vertical run is contiguous in the pointer array,
which makes scanning down far cheaper than scanning across. A coordinate with no
tile object is reported as absent rather than as id 0.
*/
func (t *TileMap) Column(x, y0, y1 int32) []Tile {
	y0, y1 = max32(0, y0), min32(t.MaxY, y1)
	if x < 0 || x >= t.MaxX || y1 <= y0 {
		return nil
	}
	idx := t.Stride*(x-t.OriginX) + (y0 - t.OriginY)
	blob := t.Mem.Read(t.Buf+entriesOff+4*uint32(idx), int(4*(y1-y0))) //nolint:gosec // an index into the buffer

	out := make([]Tile, 0, y1-y0)
	for i := range int(y1 - y0) {
		var p uint32
		if len(blob) >= 4*i+4 {
			p = binary.LittleEndian.Uint32(blob[4*i:])
		}
		if p == 0 {
			out = append(out, Tile{})
			continue
		}
		raw := t.Mem.Read(p+tileTypeOff, 2)
		if len(raw) != 2 {
			out = append(out, Tile{})
			continue
		}
		out = append(out, Tile{Type: binary.LittleEndian.Uint16(raw), Present: true})
	}
	return out
}

// Tile is one entry of a column: its id, and whether there was an object to read
// it from.
type Tile struct {
	Type    uint16
	Present bool
}

// Point is a coordinate in the world.
type Point struct{ X, Y int32 }

/*
FindType is every coordinate in the world holding a tile id.

Whole-world by design: it answers "is one of these placed *anywhere* in the world
the player is in", which no bounded search around the player can answer. It is
far too much work for a timer, so callers run it once per world load and keep the
answer.

limit stops after that many hits; a caller that only needs to know whether one
exists passes 1 and pays for a fraction of the world.
*/
func (t *TileMap) FindType(want uint16, limit int) []Point {
	var out []Point
	for x := int32(0); x < t.MaxX; x++ {
		idx := t.Stride*(x-t.OriginX) - t.OriginY
		blob := t.Mem.Read(t.Buf+entriesOff+4*uint32(idx), int(4*t.MaxY)) //nolint:gosec // an index into the buffer
		if len(blob) < int(4*t.MaxY) {
			continue
		}
		ptrs := make([]uint32, t.MaxY)
		for i := range ptrs {
			ptrs[i] = binary.LittleEndian.Uint32(blob[4*i:])
		}
		// Split into runs the pool laid out contiguously, so each can be read in
		// one go.
		start := 0
		for i := 1; i <= len(ptrs); i++ {
			if i < len(ptrs) && int64(ptrs[i])-int64(ptrs[i-1]) == tileRecord {
				continue
			}
			for _, y := range t.runMatches(ptrs[start:i], want) {
				out = append(out, Point{X: x, Y: int32(start + y)}) //nolint:gosec // a world coordinate
				if limit > 0 && len(out) >= limit {
					return out
				}
			}
			start = i
		}
	}
	return out
}

/*
runMatches is the offsets within one contiguous run whose tile id is wanted.

The whole run is read in one go when the stride holds, and otherwise a tile at a
time, so a pool that stops being contiguous degrades in speed rather than going
silently blind.
*/
func (t *TileMap) runMatches(ptrs []uint32, want uint16) []int {
	var out []int
	if len(ptrs) > 0 && ptrs[0] != 0 {
		raw := t.Mem.Read(ptrs[0], tileRecord*len(ptrs))
		if len(raw) == tileRecord*len(ptrs) {
			for i := range ptrs {
				if binary.LittleEndian.Uint16(raw[i*tileRecord+tileTypeOff:]) == want {
					out = append(out, i)
				}
			}
			return out
		}
	}
	for i, p := range ptrs {
		if p == 0 {
			continue
		}
		raw := t.Mem.Read(p+tileTypeOff, 2)
		if len(raw) == 2 && binary.LittleEndian.Uint16(raw) == want {
			out = append(out, i)
		}
	}
	return out
}

/*
Flood is every tile of the same id contiguous with a starting point, when that
id is one the caller allows.

Matching on the **starting tile's own id** rather than on "any allowed id" is
what stops a copper vein touching an iron one from taking both: a vein is one
ore. Matching goes through SolidTypeAt, so only tiles that are really there join
it.

Nothing comes back when the start is not allowed. The cap is a safety rail rather
than a performance one -- a real vein is tens of tiles, and stopping early is far
better than a mistake that strips a region.
*/
func Flood(t *TileMap, x, y int32, allowed map[int]bool, limit int, diagonal bool) []Point {
	want, ok := t.SolidTypeAt(x, y)
	if !ok || !allowed[int(want)] {
		return nil
	}
	steps := []Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	if diagonal {
		steps = append(steps, Point{1, 1}, Point{1, -1}, Point{-1, 1}, Point{-1, -1})
	}
	seen := map[Point]bool{{X: x, Y: y}: true}
	out := []Point{{X: x, Y: y}}
	queue := []Point{{X: x, Y: y}}

	for len(queue) > 0 && len(out) < limit {
		// Taken from the end, as the Python's own pop() does: which end decides
		// the order the tiles come back in, and a caller mining them sees it.
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, step := range steps {
			next := Point{X: cur.X + step.X, Y: cur.Y + step.Y}
			if seen[next] {
				continue
			}
			seen[next] = true
			if got, ok := t.SolidTypeAt(next.X, next.Y); ok && got == want {
				out = append(out, next)
				queue = append(queue, next)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return out
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// ActiveCheck is what a sanity-check of the active bit's offset observed.
type ActiveCheck struct {
	SkyActivePct        float64 `json:"sky_active_pct"`
	DeepActivePct       float64 `json:"deep_active_pct"`
	DistinctHeadersDeep int     `json:"distinct_headers_deep"`
	Sampled             int     `json:"sampled"`
	OK                  bool    `json:"ok"`
}

/*
CheckActiveOffset sanity-checks the active bit's offset against the live world.

Mono is free to lay a class out however it likes, so the offset is measured
rather than trusted -- and the failure is silent, because a wrong offset lands on
padding that reads as a constant zero and simply reports every tile as empty.

The test is two bands that must disagree: sky is open air and must be near zero
per cent active, deep rock is mostly stone and must be substantially active. A
constant-zero offset gives zero in both and fails the second.

Do not test this with "id 0 must be inactive" -- dirt *is* id 0. That premise is
what let the wrong offset through the first time.
*/
func (t *TileMap) CheckActiveOffset(x, y, width int32) ActiveCheck {
	band := func(y0, y1 int32) (active, sampled int, headers map[uint16]bool) {
		headers = map[uint16]bool{}
		for yy := max32(0, y0); yy < min32(t.MaxY, y1); yy++ {
			for xx := max32(0, x-width); xx < min32(t.MaxX, x+width); xx++ {
				p := t.entry(xx, yy)
				if p == 0 {
					continue
				}
				raw := t.Mem.Read(p+tileHeaderOff, 2)
				if len(raw) < 2 {
					continue
				}
				h := binary.LittleEndian.Uint16(raw)
				sampled++
				if h&activeBit != 0 {
					active++
				}
				headers[h] = true
			}
		}
		return active, sampled, headers
	}

	skyActive, skyN, _ := band(40, 120)
	deepActive, deepN, deepHeaders := band(y+120, y+170)
	skyPct := 100 * float64(skyActive) / float64(max(1, skyN))
	deepPct := 100 * float64(deepActive) / float64(max(1, deepN))

	return ActiveCheck{
		SkyActivePct: round1(skyPct), DeepActivePct: round1(deepPct),
		// Reported for eyeballing only: a uniform world legitimately has one
		// header, so requiring variety here would fail a world that is fine.
		DistinctHeadersDeep: len(deepHeaders),
		Sampled:             skyN + deepN,
		OK:                  skyN > 0 && deepN > 0 && skyPct < 5.0 && deepPct > 30.0,
	}
}

// round1 is a percentage as the report shows it.
func round1(v float64) float64 {
	return float64(int64(v*10+copySign(0.5, v))) / 10
}

func copySign(v, sign float64) float64 {
	if sign < 0 {
		return -v
	}
	return v
}
