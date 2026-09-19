package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
)

/*
Spawning an NPC.

This is what give_item does, one level up. The game keeps a fully-populated
template of every NPC in its sample collection, and every Main.npc slot is a real
NPC object allocated when the world loaded -- so a spawn is a field copy plus a
position. No code is injected, nothing managed is called, and nothing goes in the
build ledger.
*/

/*
playerDirection is which way the player is facing, -1 or 1.

Not in the layout package because it is not read as layout anywhere else: this is
the only caller, and it wants the sign rather than the field.
*/
const playerDirection = 0x2C

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
	base, ok := s.StaticBase()
	if !ok {
		return out
	}
	arr, ok := content.FindNPCArray(s.Mem, base)
	if !ok {
		return out
	}
	for i := range layout.MaxNPCs {
		obj, ok := s.Mem.ReadU32(arr + layout.ArrDataOff + uint32(i)*4) //nolint:gosec // a slot index
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

A template is inactive, which is what tells it from a live NPC of the same type
standing in the world with its stats already scaled.
*/
func (s *Service) npcTemplateBlock(netID int32) ([]byte, bool) {
	base, ok := s.StaticBase()
	if !ok {
		return nil, false
	}
	vt, ok := content.FindNPCVTable(s.Mem, base)
	if !ok {
		return nil, false
	}
	span := layout.NPCObjectSize
	var best []byte
	for _, region := range s.Mem.Regions() {
		buf := s.Mem.Read(region.Start, region.Size())
		for i := 0; i+4 <= len(buf); i += 4 {
			if binary.LittleEndian.Uint32(buf[i:]) != vt {
				continue
			}
			if i+span > len(buf) {
				continue
			}
			nid := int32(binary.LittleEndian.Uint32(buf[i+layout.NPCNetID:])) //nolint:gosec // a signed field
			if nid == netID && buf[i+layout.NPCActive] == 0 {
				best = buf[i : i+span]
			}
		}
	}
	return best, best != nil
}

// freeNPCSlot is the index and object address of an unused Main.npc slot.
func (s *Service) freeNPCSlot(arr uint32) (int, uint32, bool) {
	for i := range layout.MaxNPCs {
		obj, ok := s.Mem.ReadU32(arr + layout.ArrDataOff + uint32(i)*4) //nolint:gosec // a slot index
		if !ok || obj == 0 {
			continue
		}
		if active := s.Mem.Read(obj+layout.NPCActive, 1); len(active) == 1 && active[0] == 0 {
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
	base, ok := s.StaticBase()
	if !ok {
		return Spawn{}, &Error{Message: "could not find Main.npc -- is a world loaded?"}
	}
	arr, ok := content.FindNPCArray(s.Mem, base)
	if !ok {
		return Spawn{}, &Error{Message: "could not find Main.npc -- is a world loaded?"}
	}
	block, ok := s.npcTemplateBlock(netID)
	if !ok {
		return Spawn{}, &Error{
			Message: fmt.Sprintf("no template for NPC %d (%s)", netID, names.Label(int(netID))),
		}
	}
	slot, obj, ok := s.freeNPCSlot(arr)
	if !ok {
		return Spawn{}, &Error{Message: "no free NPC slot -- the world is at its NPC limit"}
	}

	player := live.LifeAddr - locate.StatLifeFromObj
	raw := s.Mem.Read(player+layout.NPCPositionX, 8)
	if len(raw) < 8 {
		return Spawn{}, &Error{Message: "the player's position is not readable"}
	}
	px := math.Float32frombits(binary.LittleEndian.Uint32(raw))
	py := math.Float32frombits(binary.LittleEndian.Uint32(raw[4:]))
	facing, _ := s.Mem.ReadI32(player + playerDirection)
	if facing == 0 {
		facing = 1
	}
	/*
		Behind the player, so a spawn never lands on top of them, and clamped
		away from the world edge, where a negative coordinate would put the NPC
		outside the map.
	*/
	x := math.Max(100.0*16, float64(px)-float64(facing)*float64(distanceTiles)*16.0)

	for _, span := range layout.NPCCopySpans {
		s.Mem.Write(obj+uint32(span[0]), block[span[0]:span[1]]) //nolint:gosec // an offset in an object
	}
	s.Mem.Write(obj+layout.NPCWhoAmI, i32Bytes(int32(slot))) //nolint:gosec // a slot index
	s.Mem.Write(obj+layout.NPCPositionX, f32Pair(float32(x), py))
	s.Mem.Write(obj+layout.NPCOldPositionX, f32Pair(float32(x), py))
	s.Mem.Write(obj+layout.NPCVelocityX, make([]byte, 16))
	s.Mem.Write(obj+layout.NPCActive, []byte{1})

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
