package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Spawning an NPC.

This is what give_item does, one level up. The game keeps a fully-populated
template of every NPC in its sample collection, and every Main.npc slot is a real
NPC object allocated when the world loaded -- so a spawn is a field copy plus a
position. No code is injected, nothing managed is called, and nothing goes in the
build ledger.
*/

// Spawn is what a spawn did, in the caller's units: tiles, not pixels.
type Spawn struct {
	Slot      int     `json:"slot"`
	ID        int32   `json:"id"`
	Name      string  `json:"name"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	TilesAway int     `json:"tiles_away"`
}

/*
liveNPCAddrs is every object Main.npc currently points at.

Excluded from the template scan because a live NPC's stats have been scaled by
the world's difficulty -- a Blue Slime in an expert world reads 60 life where its
template says 25 -- so one of those taken for a template would publish the wrong
numbers for the whole type.
*/
func (s *Service) liveNPCAddrs() map[uint32]bool {
	out := map[uint32]bool{}
	entry, _, _ := s.Support()
	base, ok := s.npcBase(entry)
	if !ok {
		return out
	}
	arr, ok := content.FindNPCArray(s.Mem, entry, base)
	if !ok {
		return out
	}
	data := arr + uint32(entry.Shapes.ArrDataOff) //nolint:gosec // a header offset
	for i := range layout.MaxNPCs {
		obj, ok := s.Mem.ReadU32(data + uint32(i)*4) //nolint:gosec // a slot index
		if ok && obj != 0 {
			out[obj] = true
		}
	}
	return out
}

/*
npcTemplateBlock is the sample-collection template for one netID, whole.

Rescanned rather than remembered by address, for the same reason the item
templates are: the managed heap is collected, so an address that held the object
stops being it. The parsed stats are cached instead, because those are numbers.

The template is the one the compendium reads: an object of the NPC class that
Main.npc does not hold -- a live NPC's stats are already scaled, and a slot an
NPC has left keeps its fields -- taken from the table where each net id appears
once. **Not** "the inactive one": that was the rule here, and on .NET Framework
2402 of the 2405 NPC objects outside Main.npc read active, the templates among
them (live game, 2026-10-11), so it found no template for anything.

The block's active byte is cleared, whatever the template's was. The byte lies
inside a copy span, and a slot must not go live until the spawn has finished
with it.
*/
func (s *Service) npcTemplateBlock(entry layout.Entry, arr uint32, netID int32) ([]byte, bool) {
	vt, ok := content.NPCVTableOf(s.Mem, entry, arr)
	if !ok {
		return nil, false
	}
	addr, ok := content.NPCTemplateAddrs(s.Mem, vt, s.liveNPCAddrs(), entry.NPC)[netID]
	if !ok {
		return nil, false
	}
	block := s.Mem.Read(addr, entry.NPC.Size)
	if len(block) < entry.NPC.Size || binary.LittleEndian.Uint32(block) != vt {
		return nil, false
	}
	if nid := int32(binary.LittleEndian.Uint32(block[entry.NPC.NetID:])); nid != netID { //nolint:gosec // a signed field
		return nil, false
	}
	block[entry.NPC.Active] = 0
	return block, true
}

// freeNPCSlot is the index and object address of an unused Main.npc slot.
func (s *Service) freeNPCSlot(entry layout.Entry, arr uint32) (int, uint32, bool) {
	data := arr + uint32(entry.Shapes.ArrDataOff) //nolint:gosec // a header offset
	for i := range layout.MaxNPCs {
		obj, ok := s.Mem.ReadU32(data + uint32(i)*4) //nolint:gosec // a slot index
		if !ok || obj == 0 {
			continue
		}
		if active := s.Mem.Read(obj+uint32(entry.NPC.Active), 1); len(active) == 1 && active[0] == 0 { //nolint:gosec // a field offset
			return i, obj, true
		}
	}
	return 0, 0, false
}

/*
SpawnNPC copies an NPC's template over a free slot and puts it beside the player.

`active` is written last on purpose: until it is set the game skips the slot
entirely, so it never sees a half-built NPC.
*/
func (s *Service) SpawnNPC(netID int32, distanceTiles int) (Spawn, error) {
	names, err := game.NPCs()
	if err != nil {
		return Spawn{}, &Error{Message: err.Error()}
	}
	live, err := s.LiveBlock()
	if err != nil {
		return Spawn{}, err
	}
	entry, _, _ := s.Support()
	base, ok := s.npcBase(entry)
	if !ok {
		return Spawn{}, &Error{Message: "could not find Main.npc -- is a world loaded?"}
	}
	arr, ok := content.FindNPCArray(s.Mem, entry, base)
	if !ok {
		return Spawn{}, &Error{Message: "could not find Main.npc -- is a world loaded?"}
	}
	block, ok := s.npcTemplateBlock(entry, arr, netID)
	if !ok {
		return Spawn{}, &Error{
			Message: fmt.Sprintf("no template for NPC %d (%s)", netID, names.Label(int(netID))),
		}
	}
	slot, obj, ok := s.freeNPCSlot(entry, arr)
	if !ok {
		return Spawn{}, &Error{Message: "no free NPC slot -- the world is at its NPC limit"}
	}

	fromLife := func(off int) uint32 { return uint32(int(live.LifeAddr) + off) } //nolint:gosec // a field near statLife
	raw := s.Mem.Read(fromLife(entry.Player.PositionFromLife), 8)
	if len(raw) < 8 {
		return Spawn{}, &Error{Message: "the player's position is not readable"}
	}
	px := math.Float32frombits(binary.LittleEndian.Uint32(raw))
	py := math.Float32frombits(binary.LittleEndian.Uint32(raw[4:]))
	facing, _ := s.Mem.ReadI32(fromLife(entry.Spawn.PlayerDirectionFromLife))
	if facing == 0 {
		facing = 1
	}
	/*
		Behind the player, so a spawn never lands on top of them, and clamped
		away from the world edge, where a negative coordinate would put the NPC
		outside the map.
	*/
	x := math.Max(100.0*16, float64(px)-float64(facing)*float64(distanceTiles)*16.0)

	at := func(off int) uint32 { return obj + uint32(off) } //nolint:gosec // an offset in an object
	for _, span := range entry.Spawn.CopySpans {
		s.Mem.Write(at(span[0]), block[span[0]:span[1]])
	}
	s.Mem.Write(at(entry.Spawn.WhoAmI), i32Bytes(int32(slot))) //nolint:gosec // a slot index
	s.Mem.Write(at(entry.Spawn.Position), f32Pair(float32(x), py))
	s.Mem.Write(at(entry.Spawn.Velocity), make([]byte, 8))
	s.Mem.Write(at(entry.Spawn.OldPosition), f32Pair(float32(x), py))
	s.Mem.Write(at(entry.NPC.Active), []byte{1})

	return Spawn{
		Slot: slot, ID: netID, Name: names.Label(int(netID)),
		X: x / 16.0, Y: float64(py) / 16.0, TilesAway: distanceTiles,
	}, nil
}

func i32Bytes(v int32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(v)) //nolint:gosec // as its bits
	return out
}

func f32Pair(a, b float32) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint32(out, math.Float32bits(a))
	binary.LittleEndian.PutUint32(out[4:], math.Float32bits(b))
	return out
}
