package service_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Spawning an NPC and editing projectiles under .NET Framework (spec 052).

Every number planted here is a literal from the measurement (cmd/clrfields
against 1.4.5.8 on .NET Framework 4.8.1, docs/clr-fields-1.4.5.8.txt), not read
from internal/layout:

  - Entity: whoAmI 0x004, direction 0x00C, width 0x010, height 0x014, position
    0x020, velocity 0x028, oldPosition 0x030, oldVelocity 0x038.
  - NPC: fourteen references at 0x050..0x087; type 0x0F4, damage 0x108, defense
    0x10C, lifeMax 0x120, netID 0x14C, active 0x18C; the object ends at 0x20C
    (BaseSize 0x210 on the live game, 2026-10-11, less the sync block).
  - The game's NPC templates read active: 761 of the 762 the scan chose on the
    live game (2026-10-11), the Gold Bunny's among them. So the template here is
    planted active.
  - Projectile: scale 0x078, type 0x080, penetrate 0x0B4, maxPenetrate 0x0B8,
    active 0x102. Item: shoot 0x0BC.
  - Player: statLife 0x470.
*/

const (
	csSpawnSlot = 3
	csNPCEnd    = 0x20C
)

// csSlotObj is the object behind a Main.npc slot, as csNPCs plants them.
func csSlotObj(slot int) uint32 { return csNPCObjects + uint32(slot)*0x220 } //nolint:gosec // a slot index

/*
csSpawnable is a CLR world to spawn a Blue Slime into: Main.npc with its first
three slots taken, the player at (8000, 4000) facing the given way, and both the
template and the slot that will be taken filled edge to edge with bytes of their
own -- 0x11 in the template, 0xEE in the slot -- so that every byte of the slot
afterwards says which of the two it came from.
*/
func csSpawnable(t *testing.T, facing int32) (memtest.CLRWorld, *service.Service, []byte) {
	t.Helper()
	w, svc := csGame(t)
	csInventories(w, nil)
	csNPCs(w)
	for slot := range csSpawnSlot {
		w.Mem.PokeBytes(csSlotObj(slot)+0x18C, []byte{1})
	}

	fill := func(at uint32, with byte) {
		block := make([]byte, csNPCEnd-4)
		for i := range block {
			block[i] = with
		}
		w.Mem.PokeBytes(at+4, block)
	}
	fill(csNPCTemplates, 0x11)
	w.Mem.PokeI32(csNPCTemplates+0x0F4, 1)
	w.Mem.PokeI32(csNPCTemplates+0x14C, 1)
	w.Mem.PokeI32(csNPCTemplates+0x120, 25)
	w.Mem.PokeI32(csNPCTemplates+0x108, 7)
	w.Mem.PokeI32(csNPCTemplates+0x10C, 2)
	w.Mem.PokeI32(csNPCTemplates+0x010, 24)
	w.Mem.PokeI32(csNPCTemplates+0x014, 18)
	w.Mem.PokeBytes(csNPCTemplates+0x18C, []byte{1})
	fill(csSlotObj(csSpawnSlot), 0xEE)
	w.Mem.PokeBytes(csSlotObj(csSpawnSlot)+0x18C, []byte{0})

	w.Mem.WriteF32(w.Live+0x020, 8000)
	w.Mem.WriteF32(w.Live+0x024, 4000)
	w.Mem.PokeI32(w.Live+0x00C, facing)
	return w, svc, w.Mem.Read(csNPCTemplates, csNPCEnd)
}

func csF32(t *testing.T, w memtest.CLRWorld, addr uint32) float32 {
	t.Helper()
	v, ok := w.Mem.ReadU32(addr)
	require.True(t, ok)
	return math.Float32frombits(v)
}

/*
A spawn under the CLR takes the first free slot and fills it from the template,
byte for byte, except for what is the slot's own.

The template's bytes are in the three copy spans. The slot's bytes are still in
its header, its fourteen references and its oldVelocity. whoAmI is the slot's
index, position and oldPosition are where the spawn was put, velocity is zero,
and active -- inside the last span, copied as the template's zero -- is one.
*/
func TestSpawningUnderTheCLRFillsTheSlotFromTheTemplate(t *testing.T) {
	atHome(t)
	w, svc, template := csSpawnable(t, 1)

	got, err := svc.SpawnNPC(1, 25)
	require.NoError(t, err)
	require.Equal(t, csSpawnSlot, got.Slot, "the slots already holding an NPC were not skipped")
	require.Equal(t, "Blue Slime", got.Name)
	require.InDelta(t, (8000.0-25*16)/16, got.X, 0)
	require.InDelta(t, 4000.0/16, got.Y, 0)

	obj := csSlotObj(got.Slot)
	after := w.Mem.Read(obj, csNPCEnd)
	for off := 0; off < csNPCEnd; off++ {
		var want byte
		switch {
		case off < 0x004: // the MethodTable pointer
			want = byte(uint32(csNPCMT) >> (8 * off)) //nolint:gosec // one byte of the word
		case off < 0x008, off >= 0x020 && off < 0x038: // whoAmI; position, velocity, oldPosition
			continue
		case off >= 0x038 && off < 0x040, off >= 0x050 && off < 0x088: // oldVelocity; the references
			want = 0xEE
		case off == 0x18C: // active
			want = 1
		default:
			want = template[off]
		}
		require.Equalf(t, want, after[off], "the byte at %#x", off)
	}

	require.EqualValues(t, csSpawnSlot, csI32(t, w, obj+0x004), "the NPC does not know which slot it is in")
	for name, off := range map[string]uint32{"position": 0x020, "oldPosition": 0x030} {
		require.InDeltaf(t, 8000.0-25*16, csF32(t, w, obj+off), 0, "%s.X", name)
		require.InDeltaf(t, 4000.0, csF32(t, w, obj+off+4), 0, "%s.Y", name)
	}
	require.Equal(t, make([]byte, 8), w.Mem.Read(obj+0x028, 8), "the velocity is not zero")

	for name, want := range map[uint32]int32{0x0F4: 1, 0x14C: 1, 0x120: 25, 0x108: 7, 0x10C: 2, 0x010: 24, 0x014: 18} {
		require.EqualValuesf(t, want, csI32(t, w, obj+name), "the field at %#x did not come across", name)
	}
	// The template itself, and the slots on either side, are untouched.
	require.Equal(t, template, w.Mem.Read(csNPCTemplates, csNPCEnd))
	require.EqualValues(t, 0, csI32(t, w, csSlotObj(csSpawnSlot+1)+0x0F4))
	require.Equal(t, []byte{0}, w.Mem.Read(csSlotObj(csSpawnSlot+1)+0x18C, 1))
}

// The side a CLR spawn lands on follows Entity.direction in the player.
func TestSpawningUnderTheCLRLandsBehindThePlayer(t *testing.T) {
	atHome(t)
	w, svc, _ := csSpawnable(t, -1)

	got, err := svc.SpawnNPC(1, 25)
	require.NoError(t, err)
	require.InDelta(t, (8000.0+25*16)/16, got.X, 0, "the NPC landed on the side the player faces")
	require.InDelta(t, 8000.0+25*16, csF32(t, w, csSlotObj(got.Slot)+0x020), 0)
}

// A CLR spawn writes active last, so the game never sees a slot that is live and
// half-built.
func TestSpawningUnderTheCLRSetsActiveLast(t *testing.T) {
	atHome(t)
	w, _, _ := csSpawnable(t, 1)
	rec := &recordingMem{execMem: &execMem{w.Mem}}

	got, err := service.New(rec, -1).WithRuntime(csRuntime).SpawnNPC(1, 25)
	require.NoError(t, err)
	require.Greater(t, len(rec.writes), 1)
	require.Equal(t, csSlotObj(got.Slot)+0x18C, rec.writes[len(rec.writes)-1],
		"something was written to the slot after it went live")
}

// activeWatcher records what the spawned slot's active byte holds after every
// write.
type activeWatcher struct {
	*execMem
	at   uint32
	seen []byte
}

func (m *activeWatcher) Write(addr uint32, data []byte) bool {
	ok := m.execMem.Write(addr, data)
	m.seen = append(m.seen, m.Read(m.at, 1)[0])
	return ok
}

/*
The slot does not go live when the template is copied over it, though the
template reads active and its active byte is inside the last copy span.

Which write came last says nothing about this: a spawn that copies the
template's active byte across and then writes active again at the end still ends
on the right address. So the byte is read after every write, and it is zero
until the last.
*/
func TestSpawningUnderTheCLRKeepsTheSlotInactiveUntilTheEnd(t *testing.T) {
	atHome(t)
	w, _, _ := csSpawnable(t, 1)
	watch := &activeWatcher{execMem: &execMem{w.Mem}, at: csSlotObj(csSpawnSlot) + 0x18C}

	got, err := service.New(watch, -1).WithRuntime(csRuntime).SpawnNPC(1, 25)
	require.NoError(t, err)
	require.Equal(t, csSpawnSlot, got.Slot)
	require.Greater(t, len(watch.seen), 4)
	last := len(watch.seen) - 1
	require.Equal(t, make([]byte, last), watch.seen[:last], "the slot went live before the spawn had finished")
	require.EqualValues(t, 1, watch.seen[last])
}

/*
A CLR spawn does not take an NPC standing in the world for the template, though
both read active and share a net id.

The Blue Slime in Main.npc's first slot has the life an expert world scales it
to. A spawn made from it would start at 60 life where the template says 25.
*/
func TestSpawningUnderTheCLRCopiesTheTemplateNotALiveNPC(t *testing.T) {
	atHome(t)
	w, svc, _ := csSpawnable(t, 1)
	require.EqualValues(t, 60, csI32(t, w, csSlotObj(0)+0x120), "the fixture's live slime is not scaled")

	got, err := svc.SpawnNPC(1, 25)
	require.NoError(t, err)
	require.EqualValues(t, 25, csI32(t, w, csSlotObj(got.Slot)+0x120))
}

// movedMem answers the one read of the template's own bytes with something
// else: the heap as it is after a collection has moved the object away.
type movedMem struct {
	*execMem
	now func([]byte) []byte
}

func (m *movedMem) Read(addr uint32, size int) []byte {
	got := m.execMem.Read(addr, size)
	if addr == csNPCTemplates && size == csNPCEnd {
		return m.now(got)
	}
	return got
}

/*
A template that is no longer where the scan found it is not copied.

The heap is collected between the scan and the read. What the read brings back
is checked for the NPC class and the net id asked for, and for being whole;
anything else is no template, and nothing is written.
*/
func TestSpawningUnderTheCLRChecksTheTemplateItRead(t *testing.T) {
	for name, now := range map[string]func([]byte) []byte{
		"another class's object": func(b []byte) []byte { b[0] ^= 0xFF; return b },
		"another NPC":            func(b []byte) []byte { b[0x14C] = 2; return b },
		"cut short":              func(b []byte) []byte { return b[:0x100] },
	} {
		t.Run(name, func(t *testing.T) {
			atHome(t)
			w, _, _ := csSpawnable(t, 1)
			before := w.Mem.Hex()
			mem := &movedMem{execMem: &execMem{w.Mem}, now: now}

			_, err := service.New(mem, -1).WithRuntime(csRuntime).SpawnNPC(1, 25)
			require.ErrorContains(t, err, "no template for NPC 1")
			require.Equal(t, before, w.Mem.Hex(), "something was written anyway")
		})
	}
}

// With no template of the NPC asked for, or no free slot, a CLR spawn says so
// and writes nothing.
func TestSpawningUnderTheCLRRefusesWhatItCannotDo(t *testing.T) {
	atHome(t)
	w, svc, _ := csSpawnable(t, 1)
	before := w.Mem.Hex()
	_, err := svc.SpawnNPC(999, 25)
	require.ErrorContains(t, err, "no template for NPC 999")
	require.Equal(t, before, w.Mem.Hex(), "something was written anyway")

	for slot := range memtest.CLRNPCSlots - 1 {
		w.Mem.PokeBytes(csSlotObj(slot)+0x18C, []byte{1})
	}
	before = w.Mem.Hex()
	_, err = svc.SpawnNPC(1, 25)
	require.ErrorContains(t, err, "at its NPC limit")
	require.Equal(t, before, w.Mem.Hex(), "something was written anyway")
}

/*
A template is read to the end of the CLR's NPC object and no further: one whose
last field is the last readable byte is still a template.

King Slime's, the only one the game has of it here, is planted so that it ends with the memory. A reader asking
for more of the object than the CLR lays out -- mono's NPC is longer -- would
find it short and pass it over.
*/
func TestACLRTemplateAtTheEndOfMemoryIsFound(t *testing.T) {
	atHome(t)
	w, svc, _ := csSpawnable(t, 1)
	const last = csBase + csSize - csNPCEnd
	csNPC(w, last, 50, 50, 2000)
	w.Mem.PokeBytes(last+0x18C, []byte{1})

	got, err := svc.SpawnNPC(50, 25)
	require.NoError(t, err)
	require.EqualValues(t, 2000, csI32(t, w, csSlotObj(got.Slot)+0x120))
}

/*
csFlying plants Main.projectile the CLR way: 1001 slots behind one MethodTable,
two of them in flight as type 985.
*/
func csFlying(w memtest.CLRWorld) (first, second uint32) {
	const mt, filler, objs = 0x081D09AC, csBase + 0x74000, csBase + 0x75000
	w.Mem.PokeI32(filler, mt)
	elems := make([]uint32, memtest.CLRProjectileSlots)
	for i := range elems {
		elems[i] = filler
	}
	first, second = objs, objs+0x140
	elems[4], elems[9] = first, second
	for _, obj := range []uint32{first, second} {
		w.Mem.PokeI32(obj, mt)
		w.Mem.PokeBytes(obj+0x102, []byte{1})
		w.Mem.PokeI32(obj+0x080, 985)
		w.Mem.PokeI32(obj+0x0B4, 1)
		w.Mem.PokeI32(obj+0x0B8, 1)
		w.Mem.WriteF32(obj+0x078, 1)
	}
	w.Mem.PlantCLRArray(w.Projectiles, memtest.CLRProjectileSlots, elems)
	return first, second
}

/*
A projectile tick under the CLR finds Main.projectile through its slot and holds
the overrides on what is in flight, at the CLR's offsets.
*/
func TestAProjectileTickUnderTheCLR(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, nil)
	first, second := csFlying(w)

	tick, err := svc.ProjectileTick(map[int32]map[string]float64{
		985: {"penetrate": -1, "scale": 2},
		14:  {},
	}, 0)
	require.NoError(t, err)
	require.Equal(t, 4, tick["patched"])
	require.Equal(t, map[int32]int{985: 2}, tick["types"])
	require.Equal(t, 1, tick["sweeps"])

	for _, obj := range []uint32{first, second} {
		require.EqualValues(t, -1, csI32(t, w, obj+0x0B4), "penetrate")
		require.EqualValues(t, -1, csI32(t, w, obj+0x0B8), "maxPenetrate")
		require.InDelta(t, 2.0, csF32(t, w, obj+0x078), 0, "scale")
		require.EqualValues(t, 985, csI32(t, w, obj+0x080), "the type was written")
	}
	require.Equal(t, map[string]any{"stopped": true}, svc.ProjectileStop())
}

// Which projectile an item fires is read from Item.shoot where the CLR keeps it.
func TestProjectileOfUnderTheCLR(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, []memtest.CLRItem{{Slot: 0, Type: 757, Stack: 1}})
	w.Mem.PlantCLRTemplate(csTemplates, 3531, map[uint32]int32{0x064: 1, 0x0BC: 625, 0x0FC: 77})

	got, err := svc.ProjectileOf(3531)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"item": int32(3531), "shoot": int32(625)}, got)
}
