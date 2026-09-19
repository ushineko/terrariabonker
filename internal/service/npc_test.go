package service_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Spawning is a field copy, and what matters is which fields.

A spawn that copies too much hands the slot the template's own arrays and the two
share them from then on -- which does not fail here, it fails later, in the game,
somewhere else. So the comparison is the whole image rather than the answer: every
byte either implementation wrote is checked against the other's, and the tests
below then say what the bytes have to mean.
*/

// spawnFixture is a game with a world, a player facing right and Main.npc in it.
func spawnFixture(t *testing.T) (*execMem, *service.Service) {
	t.Helper()
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantNPCsInto(mem)
	plantPositionInto(mem, 8000.0, 4000.0)
	plantFacingInto(mem, 1)
	return mem, service.New(mem, -1)
}

// spawnedObject is where the fixture put the object behind a Main.npc slot.
func spawnedObject(slot int) uint32 {
	return uint32(npcObjectsAt + slot*npcStride) //nolint:gosec // a slot index
}

/*
The slot taken is the first free one, and what lands in it is a whole NPC.

Everything here is read from the fixture rather than from the other
implementation: the differential above only says the two agree, and two
implementations that both skip the stat block agree perfectly.
*/
func TestASpawnFillsTheSlotItTook(t *testing.T) {
	mem, svc := spawnFixture(t)
	got, err := svc.SpawnNPC(1, 25)
	require.NoError(t, err)
	require.Equal(t, 3, got.Slot, "the slots already holding an NPC were not skipped")

	obj := spawnedObject(got.Slot)
	for name, want := range map[string]int32{
		"NPC_NET_ID": 1, "NPC_TYPE": 1, "NPC_LIFE_MAX": 25,
		"NPC_DAMAGE": 7, "NPC_DEFENSE": 2, "NPC_WIDTH": 24, "NPC_HEIGHT": 18,
	} {
		v, ok := mem.ReadI32(obj + uint32(layout.Offsets[name])) //nolint:gosec // an offset
		require.True(t, ok)
		require.Equalf(t, want, v, "%s did not come across from the template", name)
	}

	who, ok := mem.ReadI32(obj + layout.NPCWhoAmI)
	require.True(t, ok)
	require.Equal(t, int32(3), who, "the NPC does not know which slot it is in")

	active := mem.Read(obj+layout.NPCActive, 1)
	require.Equal(t, []byte{1}, active, "the slot was left inactive")
}

/*
The spawn lands behind the player, at the distance asked for.

Facing is read rather than assumed, so the sign is checked both ways: a spawn
that always went left would pass with one of these.
*/
func TestASpawnLandsBehindThePlayer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		facing int32
		want   float64
	}{
		{"facing right, spawn to the left", 1, (8000.0 - 25*16) / 16},
		{"facing left, spawn to the right", -1, (8000.0 + 25*16) / 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem, svc := spawnFixture(t)
			plantFacingInto(mem, tc.facing)

			got, err := svc.SpawnNPC(1, 25)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.X, "the NPC landed on the wrong side")
			require.Equal(t, 4000.0/16, got.Y, "the NPC did not land at the player's height")

			obj := spawnedObject(got.Slot)
			require.InDelta(t, got.X*16, f32At(t, mem, obj+layout.NPCPositionX), 0.01,
				"the position written is not the one reported")
			require.Zero(t, f32At(t, mem, obj+layout.NPCVelocityX),
				"the template's velocity came across")
		})
	}
}

// f32At is one float read out of the image.
func f32At(t *testing.T, mem *execMem, addr uint32) float64 {
	t.Helper()
	raw := mem.Read(addr, 4)
	require.Len(t, raw, 4)
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(raw)))
}

/*
A spawn near the world edge is clamped away from it.

A negative coordinate puts the NPC outside the map, where it is not a spawn at
all -- so the distance is what gives way, not the position.
*/
func TestASpawnIsClampedAwayFromTheWorldEdge(t *testing.T) {
	mem, svc := spawnFixture(t)
	plantPositionInto(mem, 1700.0, 4000.0)

	got, err := svc.SpawnNPC(1, 25)
	require.NoError(t, err)
	require.Equal(t, 100.0, got.X, "the NPC was put outside the map")
}

// An NPC the game has no template for is refused rather than half-spawned.
func TestSpawningAnNPCWithNoTemplate(t *testing.T) {
	mem, svc := spawnFixture(t)
	before := mem.Hex()

	_, err := svc.SpawnNPC(999, 25)
	require.ErrorContains(t, err, "no template for NPC 999")
	sameMemory(t, before, mem.Hex(), "something was written anyway")
}

// And a world already at its NPC limit is refused too.
func TestSpawningWithNoFreeSlot(t *testing.T) {
	mem, svc := spawnFixture(t)
	// Through the array rather than by arithmetic: the slots' objects are
	// allocated separately and one of them is deliberately not where the others
	// are.
	for i := range layout.MaxNPCs {
		obj, ok := mem.ReadU32(npcArrAt + layout.ArrDataOff + uint32(i)*4) //nolint:gosec // a slot index
		require.True(t, ok)
		mem.PokeBytes(obj+layout.NPCActive, []byte{1})
	}
	before := mem.Hex()

	_, err := svc.SpawnNPC(1, 25)
	require.ErrorContains(t, err, "at its NPC limit")
	sameMemory(t, before, mem.Hex(), "something was written anyway")
}

// Without a world there is no Main.npc, and the spawn says so.
func TestSpawningWithNoNPCArray(t *testing.T) {
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantPositionInto(mem, 8000.0, 4000.0)

	_, err := service.New(mem, -1).SpawnNPC(1, 25)
	require.ErrorContains(t, err, "could not find Main.npc")
}

/*
`active` is written after everything else, and that is checked as an order
rather than as a result.

Nothing about the finished slot says when each field landed, so a fixture cannot
catch this by reading memory afterwards: a spawn that sets `active` first leaves
exactly the same bytes behind. What it does not leave the same is the window in
between, where the game is looking at a slot that is live and empty. So the
writes are recorded as they are made.
*/
type recordingMem struct {
	*execMem
	writes []uint32
}

func (m *recordingMem) Write(addr uint32, data []byte) bool {
	m.writes = append(m.writes, addr)
	return m.execMem.Write(addr, data)
}

func TestASpawnSetsActiveLast(t *testing.T) {
	mem, _ := spawnFixture(t)
	rec := &recordingMem{execMem: mem}

	got, err := service.New(rec, -1).SpawnNPC(1, 25)
	require.NoError(t, err)

	obj := spawnedObject(got.Slot)
	last := rec.writes[len(rec.writes)-1]
	require.Equal(t, obj+layout.NPCActive, last,
		"something was written to the slot after it went live")
	require.Greater(t, len(rec.writes), 1, "the spawn only wrote one thing")
}
