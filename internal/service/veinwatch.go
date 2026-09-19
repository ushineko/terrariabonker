package service

import (
	"context"
	"sort"
	"time"

	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
Watching for the player breaking a whitelisted tile, and taking the rest of its
vein.

This is the shape people expect from a vein miner: break one ore by hand and the
connected run goes with it, rather than naming a coordinate up front.

Detection has to be quick or it misses the first tile. Rescanning the window
every round costs about a third of a second over thirty thousand tiles, and with
fast mining the player breaks several blocks inside that -- so the vein only went
"after a few". Instead the window is scanned once to learn where the ore *is*,
and each round re-checks only those tiles, about twelve hundred of them. The full
scan is redone when the player walks away from where it was taken, on a slow
heartbeat, and after a vein is mined.
*/

const (
	// RescanEvery is the heartbeat that catches ore revealed by someone else
	// digging.
	RescanEvery = 3 * time.Second
	// RescanMove is how far the player may drift before the window is stale.
	RescanMove = 12
	/*
		DefaultWatchRadius is the window when the tool-reach cheat is not on to
		say otherwise.

		It has to cover how far the player can actually mine rather than a number
		that looks reasonable: with tool reach they break tiles seventy-five away,
		and a smaller window silently stops triggering the moment they mine at
		range.
	*/
	DefaultWatchRadius = 90
	// ReachMargin is how far past the cheat's reach the window goes.
	ReachMargin = 15
)

// watchSteps are the eight neighbours a broken tile's vein can continue into.
var watchSteps = [8][2]int32{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1},
}

// VeinWatch is one running watch, with the detection state that has to survive
// between rounds.
type VeinWatch struct {
	svc     *Service
	p       *patch.Patcher
	gems    bool
	limit   int
	timeout time.Duration
	want    map[int]bool
	radius  int32

	tm       *tiles.TileMap
	tracked  map[[2]int32]uint16
	at       [2]int32
	haveAt   bool
	lastFull time.Time
	now      func() time.Time
}

/*
NewVeinWatch is a watcher over a game with the extractor applied.

Kept separate from any loop because the window cannot block: it drives this from
a timer a few rounds at a time, while the command line spins it. Both share the
detection state, which has to persist between rounds -- rebuilding the ore map
every call is the third of a second that made the extractor miss the first tile.
*/
func (s *Service) NewVeinWatch(p *patch.Patcher, gems bool, limit int, radius int32,
	timeout time.Duration) (*VeinWatch, error) {
	if !p.IsEnabled("ore_extract") {
		return nil, &Error{Message: "the ore extractor cheat is not enabled"}
	}
	if radius <= 0 {
		radius = DefaultWatchRadius
		if reach, tuned := p.Values()["tool_reach"]; tuned {
			radius = int32(reach) + ReachMargin
		}
	}
	tm, err := s.TileMap()
	if err != nil {
		return nil, err
	}
	return &VeinWatch{
		svc: s, p: p, gems: gems, limit: limit, timeout: timeout,
		want: tiles.Whitelist(gems), radius: radius, tm: tm,
		tracked: map[[2]int32]uint16{}, now: time.Now,
	}, nil
}

// scan is where the ore is within the window, which is what each round then
// re-checks.
func (w *VeinWatch) scan(px, py int32) map[[2]int32]uint16 {
	out := map[[2]int32]uint16{}
	for y := max32(0, py-w.radius); y < min32(w.tm.MaxY, py+w.radius); y++ {
		for x := max32(0, px-w.radius); x < min32(w.tm.MaxX, px+w.radius); x++ {
			if t, solid := w.tm.SolidTypeAt(x, y); solid && w.want[int(t)] {
				out[[2]int32{x, y}] = t
			}
		}
	}
	return out
}

// Round is one detection round. It reports one result per vein taken, which is
// usually none.
func (w *VeinWatch) Round() ([]Extracted, error) {
	px, py, err := w.svc.PlayerTile()
	if err != nil {
		return nil, err
	}
	now := w.now()
	if !w.haveAt || now.Sub(w.lastFull) > RescanEvery ||
		max32(abs32(px-w.at[0]), abs32(py-w.at[1])) > RescanMove {
		w.tracked = w.scan(px, py)
		w.at, w.haveAt, w.lastFull = [2]int32{px, py}, true, now
	}

	// The fast path: only the tiles already known to be ore.
	var broke [][2]int32
	for xy := range w.tracked {
		if _, solid := w.tm.SolidTypeAt(xy[0], xy[1]); !solid {
			broke = append(broke, xy)
		}
	}
	/*
		In coordinate order, because a map is in none.

		Two tiles of one vein broken in the same round would otherwise be taken
		in a different order run to run, and the report names the tile that
		triggered it.
	*/
	sort.Slice(broke, func(i, j int) bool {
		if broke[i][0] != broke[j][0] {
			return broke[i][0] < broke[j][0]
		}
		return broke[i][1] < broke[j][1]
	})

	out := []Extracted{}
	for _, xy := range broke {
		tid := w.tracked[xy]
		delete(w.tracked, xy)
		/*
			The tile is gone, so the vein is whatever neighbour of the same id is
			still standing: breaking copper takes copper, not the iron behind it.
		*/
		start, found := w.neighbourOf(xy, tid)
		if !found {
			continue // a lone tile: nothing connected
		}
		got, err := w.svc.ExtractVein(w.p, start[0], start[1], w.gems, w.limit, w.timeout)
		if err != nil {
			return out, err
		}
		got.TriggeredBy = &[2]int32{xy[0], xy[1]}
		out = append(out, got)
	}
	if len(broke) > 0 {
		px, py, err = w.svc.PlayerTile()
		if err != nil {
			return out, err
		}
		w.tracked = w.scan(px, py)
		w.at, w.haveAt, w.lastFull = [2]int32{px, py}, true, w.now()
	}
	return out, nil
}

// neighbourOf is a tile of the same kind still standing beside a broken one.
func (w *VeinWatch) neighbourOf(xy [2]int32, tid uint16) ([2]int32, bool) {
	for _, step := range watchSteps {
		q := [2]int32{xy[0] + step[0], xy[1] + step[1]}
		if t, solid := w.tm.SolidTypeAt(q[0], q[1]); solid && t == tid {
			return q, true
		}
	}
	return [2]int32{}, false
}

// Close disarms the extractor. A queue left armed is re-mined on every frame.
func (w *VeinWatch) Close() { w.p.OreQueue().Disarm() }

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// max32 is the larger of two coordinates, beside min32 in sell.go.
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// WatchResult is what a run of the watcher took.
type WatchResult struct {
	Rounds int         `json:"rounds"`
	Mined  int         `json:"mined"`
	Events []Extracted `json:"events"`
}

/*
WatchTick runs the watcher for up to a budget and reports what it took.

The window drives this from a timer. It runs several rounds per call rather than
one because a round costs about twenty milliseconds while a round trip to the
privileged worker costs rather more -- so batching them keeps detection tight
without a timer firing at fifty hertz across a process boundary.
*/
func (s *Service) WatchTick(p *patch.Patcher, gems bool, limit int,
	timeout, budget time.Duration) (WatchResult, error) {
	if s.watch == nil {
		w, err := s.NewVeinWatch(p, gems, limit, 0, timeout)
		if err != nil {
			return WatchResult{}, err
		}
		s.watch = w
	}
	out := WatchResult{Events: []Extracted{}}
	end := time.Now().Add(budget)
	for time.Now().Before(end) {
		out.Rounds++
		got, err := s.watch.Round()
		out.Events = append(out.Events, got...)
		if err != nil {
			return out, err
		}
		if len(out.Events) > 0 {
			break // hand results back promptly
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, e := range out.Events {
		out.Mined += e.Mined
	}
	return out, nil
}

// WatchStop drops the watcher and disarms. Called when the cheat is switched
// off.
func (s *Service) WatchStop() bool {
	w := s.watch
	s.watch = nil
	if w == nil {
		return false
	}
	w.Close()
	return true
}

/*
WatchVeins mines the rest of a vein whenever the player breaks one of its tiles,
until it is told to stop.

Blocking, for the command line; the window uses WatchTick instead. `rounds` of
zero means until the context is cancelled.
*/
func (s *Service) WatchVeins(ctx context.Context, p *patch.Patcher, gems bool, limit int,
	radius int32, timeout time.Duration, rounds int, onEvent func(Extracted)) (WatchResult, error) {
	w, err := s.NewVeinWatch(p, gems, limit, radius, timeout)
	if err != nil {
		return WatchResult{}, err
	}
	defer w.Close()

	out := WatchResult{Events: []Extracted{}}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := w.Round()
		for _, e := range got {
			out.Mined += e.Mined
			out.Events = append(out.Events, e)
			if onEvent != nil {
				onEvent(e)
			}
		}
		if err != nil {
			return out, err
		}
		if !sleepCtx(ctx, 10*time.Millisecond) {
			return out, nil
		}
	}
	return out, nil
}

/*
sleepCtx waits, and reports whether the wait finished rather than being
cancelled.

Every loop here is one somebody stops by closing the window or pressing ^C, and
a plain sleep makes them wait out the interval before that is noticed.
*/
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
