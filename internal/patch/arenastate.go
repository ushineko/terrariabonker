package patch

import "encoding/binary"

/*
The data each stub reads out of the arena, and the trainer's side of it.

A stub is a dozen instructions; the interesting half of a code-patch cheat is the
words in the arena it reads -- an arm flag, a queue of coordinates, a counter it
writes back. That is *per-cheat protocol*, and it had accumulated on the patcher
as eight methods, so adding a cheat meant editing the generic patch engine.

Each type here owns one cheat's slice of the arena: its offsets, its layout, and
the ordering rules that make a half-written state safe. The patcher hands them
out and knows nothing about what is in them.

**The offsets must not overlap.** They are all offsets into one 64 KB block, and
auto-use's arm flag was once placed inside the extractor's queue: mining a vein
wrote the tile count into the arm word, and the stub pressed the player's use
button for every batch queued. Nothing in the auto-use code was involved, which
is what made it baffling in the log. A test computes the extents and fails on an
overlap.
*/

// arenaView is a cheat's words in the arena, or nothing at all when there is no
// arena yet.
type arenaView struct {
	mem   ArenaMem
	arena uint32
}

// at is the address of one of this cheat's words, and whether there is one.
func (v arenaView) at(off uint32) (uint32, bool) {
	if v.arena == 0 {
		return 0, false
	}
	return v.arena + off, true
}

// AutoUse arms a one-shot press. The stub consumes the flag and presses on the
// next frame.
type AutoUse struct{ arenaView }

// AutoUse is the auto-use cheat's words in this game's arena.
func (p *Patcher) AutoUse() AutoUse {
	return AutoUse{arenaView{mem: p.Mem, arena: p.arena}}
}

/*
Arm asks for one press on the next frame.

Arming twice before a frame runs is one press, not two: the stub consumes the
flag rather than counting it. The flag means "press soon", so a caller that wants
several presses has to wait for each to land.
*/
func (a AutoUse) Arm() bool { return a.write(AutoUseArmedOff, 1) }

/*
Disarm drops a press that has not landed yet.

An arm is a promise to press on the *next frame*, and frames stop -- at the title
screen, in a menu, on a world load. Left set, the flag waits and fires the moment
the game updates again: a press the player did not ask for, arriving as their
character appears. Found by arming fifty times at the menu and watching nothing
consume any of them.
*/
func (a AutoUse) Disarm() bool { return a.write(AutoUseArmedOff, 0) }

// Armed reports whether a press is still waiting for a frame. It clears itself
// when the stub runs.
func (a AutoUse) Armed() bool { return a.read(AutoUseArmedOff) != 0 }

/*
Presses is how many presses the stub has made since the arena was allocated.

The stub's own count, not this side's, which is what makes it evidence: a caller
that armed several times and reads back fewer knows the presses did not happen
rather than assuming they did.
*/
func (a AutoUse) Presses() int32 { return a.read(AutoUseCountOff) }

// write puts a word in one of this cheat's slots.
func (v arenaView) write(off uint32, value int32) bool {
	addr, ok := v.at(off)
	if !ok {
		return false
	}
	return v.mem.Write(addr, binary.LittleEndian.AppendUint32(nil, uint32(value))) //nolint:gosec // a word, as its bits
}

// read is one of this cheat's words, or zero when there is no arena.
func (v arenaView) read(off uint32) int32 {
	addr, ok := v.at(off)
	if !ok {
		return 0
	}
	b := v.mem.Read(addr, 4)
	if len(b) < 4 {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(b)) //nolint:gosec // a word, as its bits
}

// Tile is one coordinate pair in the extractor's queue.
type Tile struct{ X, Y int32 }

// OreQueue is the tiles the extractor stub will mine on its next frame.
type OreQueue struct{ arenaView }

// OreQueue is the extractor's queue in this game's arena.
func (p *Patcher) OreQueue() OreQueue {
	return OreQueue{arenaView{mem: p.Mem, arena: p.arena}}
}

/*
Address is where the queue is, and whether there is one yet.

A fixed offset into memory this program allocated, rather than the tail of a
borrowed cave: an address, with no derivation from a stub's length and no risk of
drifting away from what the stub reads.
*/
func (q OreQueue) Address() (uint32, bool) { return q.at(OreQueueOff) }

/*
Armed reports whether anything is queued.

It reports what *this* side last set, because only this side writes it. Whether
those tiles actually got mined is answered by looking at the tiles.
*/
func (q OreQueue) Armed() bool { return q.read(OreQueueOff) != 0 }

/*
Arm queues up to OreMaxBatch tiles, and is how many were taken.

The count is written **last**, so the game can never see a count covering
coordinates that are only half written: the stub would mine whatever happened to
be there, and mining the wrong tile cannot be undone.
*/
func (q OreQueue) Arm(tiles []Tile) int {
	at, ok := q.Address()
	if !ok {
		return 0
	}
	if len(tiles) > OreMaxBatch {
		tiles = tiles[:OreMaxBatch]
	}
	if len(tiles) == 0 {
		return 0
	}
	pairs := make([]byte, 0, len(tiles)*8)
	for _, t := range tiles {
		pairs = binary.LittleEndian.AppendUint32(pairs, uint32(t.X)) //nolint:gosec // a coordinate, as its bits
		pairs = binary.LittleEndian.AppendUint32(pairs, uint32(t.Y)) //nolint:gosec // a coordinate, as its bits
	}
	q.mem.Write(at+4, pairs)
	q.write(OreQueueOff, int32(len(tiles))) //nolint:gosec // bounded by OreMaxBatch
	return len(tiles)
}

// Disarm stops the stub mining. A queue left armed is re-mined on every frame.
func (q OreQueue) Disarm() bool {
	if _, ok := q.Address(); !ok {
		return false
	}
	return q.write(OreQueueOff, 0)
}
