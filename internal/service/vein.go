package service

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
Mining a vein through the game's own tile-breaking code.

The unprivileged side does the thinking -- read the tile map, flood-fill the vein,
decide what may be taken -- and hands the coordinates to a stub a batch at a time.
Everything here is about not taking more than was asked for.
*/

// Vein is what a miner would take, or did.
type Vein struct {
	At          [2]int32   `json:"at"`
	Type        *uint16    `json:"type"`
	Name        string     `json:"name"`
	Whitelisted bool       `json:"whitelisted"`
	Tiles       [][2]int32 `json:"tiles"`
	Count       int        `json:"count"`
	Capped      bool       `json:"capped"`
	World       [2]int32   `json:"world"`
}

/*
VeinAt is what a miner *would* take, starting at one tile. It reads only.

Deliberately a dry run: nothing about mining is undoable, so the part that
decides which tiles to take is worth being able to inspect on its own.
*/
func (s *Service) VeinAt(x, y int32, gems bool, limit int, diagonal bool) (Vein, error) {
	tm, err := s.TileMap()
	if err != nil {
		return Vein{}, err
	}
	if limit <= 0 {
		limit = tiles.DefaultLimit
	}
	allowed := tiles.Whitelist(gems)

	out := Vein{At: [2]int32{x, y}, Tiles: [][2]int32{}, World: [2]int32{tm.MaxX, tm.MaxY}}
	if t, ok := tm.TypeAt(x, y); ok {
		out.Type = &t
		out.Name = tileName(int(t))
		out.Whitelisted = allowed[int(t)]
	}
	for _, p := range tiles.Flood(tm, x, y, allowed, limit, diagonal) {
		out.Tiles = append(out.Tiles, [2]int32{p.X, p.Y})
	}
	out.Count = len(out.Tiles)
	out.Capped = out.Count >= limit
	return out, nil
}

// tileName is what a tile id is called, across every set a miner may take from.
func tileName(id int) string {
	for _, set := range []map[int]string{tiles.Ores, tiles.Extractables, tiles.WorldFormed, tiles.Gems} {
		if name, ok := set[id]; ok {
			return name
		}
	}
	return ""
}

/*
PlayerTile is the tile the player is standing in.

Their position is in world pixels at sixteen to the tile, and it is held on the
player object rather than anywhere the block reader reaches.
*/
func (s *Service) PlayerTile() (int32, int32, error) {
	live, err := s.LiveBlock()
	if err != nil {
		return 0, 0, err
	}
	base := live.LifeAddr - locate.StatLifeFromObj
	raw := s.Mem.Read(base+0x0C, 8)
	if len(raw) < 8 {
		return 0, 0, &Error{Message: "the player's position is not readable"}
	}
	px := math.Float32frombits(binary.LittleEndian.Uint32(raw))
	py := math.Float32frombits(binary.LittleEndian.Uint32(raw[4:]))
	return int32(math.Floor(float64(px) / 16)), int32(math.Floor(float64(py) / 16)), nil
}

/*
FallSearchReads is the budget spent looking for a deposit that has moved.

Tiles that fall cannot be identified from a table anybody can trust: the game's
own list of falling tiles holds four sands, yet silt and slush demonstrably fall
too, by some other mechanism. So the search does not classify tiles at all -- it
looks down the column and spends a fixed budget doing it, which is correct for
any falling tile including ones nobody has identified.
*/
const FallSearchReads = 40000

/*
Regrowth is a finder for "where has this vein settled?", given the tiles it first
held.

Exported because it is the rule with the history: it is what stops the extractor
mining an unrelated deposit, and it is worth being able to ask on its own rather
than only through a mining run.

Silt, slush and sand fall when the tile under them goes, so between batches the
coordinates from the first flood are stale: the blocks are still there, lower
down. Gravity is vertical, so a falling tile stays in its **own column** and can
only move down -- searching a box instead finds unrelated deposits of the same ore
below the vein and mines those too, which is ore nobody asked for.

The search walks each column the vein occupied, downwards from the vein's topmost
tile there, and **stops at the first solid tile that is not ours**: a falling pile
rests on top of the ground it lands on, so anything under that ground is a
different deposit. Without that stop the search runs to the world floor and
happily mines an unrelated patch a long way below -- which is what "it mines
non-contiguous sections across the screen" turned out to be.
*/
func Regrowth(tm *tiles.TileMap, first []tiles.Point, tid uint16) func() (tiles.Point, bool) {
	top := map[int32]int32{}
	for _, p := range first {
		if y, seen := top[p.X]; !seen || p.Y < y {
			top[p.X] = p.Y
		}
	}
	rows := int32(max(8, FallSearchReads/max(1, len(top)))) //nolint:gosec // a read budget

	// In column order, so the same tile is found first every time.
	columns := make([]int32, 0, len(top))
	for x := range top {
		columns = append(columns, x)
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i] < columns[j] })

	return func() (tiles.Point, bool) {
		for _, x := range columns {
			from := top[x]
			for y := max(int32(0), from); y < min(tm.MaxY, from+rows); y++ {
				t, there := tm.SolidTypeAt(x, y)
				if there && t == tid {
					return tiles.Point{X: x, Y: y}, true
				}
				if there {
					break // ground: the pile cannot be below this
				}
			}
		}
		return tiles.Point{}, false
	}
}

// Drained is what draining a vein did.
type Drained struct {
	Mined   int
	Batches int
	Waits   []float64
	Stalled string
}

/*
drain hands the vein to the stub a batch at a time until it is gone or stops
going.

The stub only runs while the game is updating, so each batch waits for tiles to
actually be **gone** rather than for the stub to acknowledge anything -- it only
reads the queue. Waiting on the tiles is the better test regardless: it is the
success condition rather than a proxy for it.
*/
func (s *Service) drain(p *patch.Patcher, tm *tiles.TileMap, standing func() (tiles.Point, bool),
	tid uint16, budget int, timeout time.Duration) Drained {
	out := Drained{Waits: []float64{}}
	queue := p.OreQueue()
	defer queue.Disarm() // a queue left armed is re-mined every frame

	for out.Mined < budget {
		seed, ok := standing()
		if !ok {
			break // the whole deposit is gone
		}
		batch := tiles.Flood(tm, seed.X, seed.Y, map[int]bool{int(tid): true},
			budget-out.Mined, true)
		if len(batch) > patch.OreMaxBatch {
			batch = batch[:patch.OreMaxBatch]
		}
		if len(batch) == 0 {
			break
		}
		queued := make([]patch.Tile, len(batch))
		for i, b := range batch {
			queued[i] = patch.Tile{X: b.X, Y: b.Y}
		}
		if queue.Arm(queued) == 0 {
			out.Stalled = "could not arm the stub"
			break
		}
		out.Batches++

		started := time.Now()
		for time.Since(started) < timeout {
			if allGone(tm, batch) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		done := 0
		for _, q := range batch {
			if _, there := tm.SolidTypeAt(q.X, q.Y); !there {
				done++
			}
		}
		out.Waits = append(out.Waits, round3(time.Since(started).Seconds()))
		out.Mined += done
		if done == 0 {
			out.Stalled = fmt.Sprintf(
				"stopped early -- nothing in a batch of %d broke within %.0fs",
				len(batch), timeout.Seconds())
			break
		}
	}
	return out
}

// allGone reports whether every tile of a batch has been taken.
func allGone(tm *tiles.TileMap, batch []tiles.Point) bool {
	for _, q := range batch {
		if _, there := tm.SolidTypeAt(q.X, q.Y); there {
			return false
		}
	}
	return true
}

// Extracted is what mining a vein did.
type Extracted struct {
	At         [2]int32  `json:"at"`
	Queued     int       `json:"queued"`
	Mined      int       `json:"mined"`
	Left       int       `json:"left"`
	Batches    int       `json:"batches"`
	Waits      []float64 `json:"waits"`
	MedianWait *float64  `json:"median_wait"`
	Reason     string    `json:"reason"`
}

/*
ExtractVein mines the vein at a coordinate through the game's own tile breaking.

Flood for what is there, drain it a batch at a time, report what happened. The
batch is capped because draining a whole vein in a single frame would run
hundreds of tile breaks at once, each spawning dust, drops and light updates.

**The vein is re-found between batches rather than remembered** -- see Regrowth.
Ores do not move, so for them this is the same list twice and costs one extra
flood.
*/
func (s *Service) ExtractVein(p *patch.Patcher, x, y int32, gems bool, limit int,
	timeout time.Duration) (Extracted, error) {
	if !p.IsEnabled("ore_extract") {
		return Extracted{}, &Error{Message: "the ore extractor cheat is not enabled"}
	}
	tm, err := s.TileMap()
	if err != nil {
		return Extracted{}, err
	}
	if limit <= 0 {
		limit = tiles.DefaultLimit
	}
	first := tiles.Flood(tm, x, y, tiles.Whitelist(gems), limit, true)
	out := Extracted{At: [2]int32{x, y}, Waits: []float64{}}
	if len(first) == 0 {
		out.Reason = "not a whitelisted tile"
		return out, nil
	}
	tid, _ := tm.SolidTypeAt(x, y)
	standing := Regrowth(tm, first, tid)

	/*
		A vein cannot grow. Whatever the re-scan turns up, never take more tiles
		than the vein that was asked for held -- that is the backstop against
		following one deposit into another.
	*/
	got := s.drain(p, tm, standing, tid, min(limit, len(first)), timeout)

	out.Queued, out.Mined, out.Batches = len(first), got.Mined, got.Batches
	out.Waits, out.Reason = got.Waits, got.Stalled
	if _, left := standing(); left {
		out.Left = -1
	} else {
		out.Left = len(first) - got.Mined
	}
	if len(got.Waits) > 0 {
		sorted := append([]float64{}, got.Waits...)
		sort.Float64s(sorted)
		median := round3(sorted[len(sorted)/2])
		out.MedianWait = &median
	}
	return out, nil
}

// round3 is a duration as the report shows it.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
