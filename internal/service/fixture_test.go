package service_test

import (
	"fmt"
	"strings"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
A game with two player copies in it, planted into both implementations.

This is the situation the whole package exists for: the heap holds the live
player and an inert load-time snapshot, they read alike, and every rule about
which one to write to and which one to believe is silent when broken. So the
image has both, with *different* inventories -- the snapshot holds what it held
when it was taken -- and a get_LocalPlayer that points at one of them.

There are two snapshots, one either side of the live copy, and that placement is
load-bearing. A scan finds the lower one first, which is what the live game did
when this was measured, so a reader that took the first copy is caught; and the
higher one is written last, so a writer that reported whichever copy it finished
with is caught too. With the live copy at either end, both of those pass by
accident.
*/

const (
	base = 0x10000000
	size = 0x40000

	// The two inert snapshots, and the live player behind get_LocalPlayer.
	snapLife  = base + 0x3000
	snap2Life = base + 0x2A000
	liveObj   = base + 0x8000
	liveLife  = liveObj + 0x738 // locate.StatLifeFromObj

	snapName = base + 0x40
	liveName = base + 0x80

	// get_LocalPlayer and the two statics it reads.
	code           = base + 0x1000
	playerStatic   = base + 0x100
	myPlayerStatic = base + 0x104
	playerArray    = base + 0x200
	myPlayer       = 2

	// Each copy's Item[] and the objects in it.
	snapArr    = base + 0x20000
	snapItems  = base + 0x24000
	snap2Arr   = base + 0x2C000
	snap2Items = base + 0x30000
	liveArr    = base + 0x10000
	liveItems  = base + 0x14000
	itemStride = 0x400
)

/*
The two copies' life and mana, chosen so that neither fallback can find the live
one.

Both are below their life cap, so the activity guess has two candidates and
refuses; and the snapshot holds at least as many items as the live player, so
the richest-inventory fallback picks the snapshot. Only ground truth gets this
right, which is the point: with a copy that either fallback would have found,
the tests pass whether the resolver is consulted or not. It was not, at first,
and they did.
*/
var (
	liveBlock  = []int32{500, 500, 137, 200, 220, 220}
	snapBlock  = []int32{500, 500, 300, 200, 220, 220}
	snap2Block = []int32{500, 500, 260, 200, 220, 220}
)

/*
snap2ItemsImage is the second snapshot, whose inventory is different again.

It is the copy written last, so a sweep that reported what it finished with
would report these slots.
*/
var snap2ItemsImage = []item{
	{slot: 2, fields: []field{
		{"ITEM_TYPE", "i32", 3509}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 35},
	}},
	{slot: 8, fields: []field{
		{"ITEM_TYPE", "i32", 9}, {"ITEM_STACK", "i32", 50},
	}},
}

// field is one value planted into an item, named as the Python names it.
type field struct {
	name  string
	kind  string // "i32", "u8" or "f32"
	value float64
}

// item is one slot: which slot, and what is in it. A slot with no fields at all
// has no object, which is not the same as an empty slot.
type item struct {
	slot   int
	absent bool
	fields []field
}

// liveItemsImage is the live player's inventory: a pickaxe, a favorited potion,
// a rod, a sword, an empty slot and a slot with nothing in it.
var liveItemsImage = []item{
	{slot: 0, fields: []field{
		{"ITEM_TYPE", "i32", 3509}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 100}, {"ITEM_USE_TIME", "i32", 20},
		{"ITEM_USE_ANIM", "i32", 25}, {"ITEM_DAMAGE", "i32", 8},
		{"ITEM_RARE", "i32", 1}, {"ITEM_AUTOREUSE", "u8", 1},
		{"ITEM_MELEE", "u8", 1},
	}},
	{slot: 1, fields: []field{
		{"ITEM_TYPE", "i32", 188}, {"ITEM_STACK", "i32", 5},
		{"ITEM_FAVORITED", "u8", 1}, {"ITEM_CONSUMABLE", "u8", 1},
		{"ITEM_BUFF_TYPE", "i32", 11},
	}},
	{slot: 2, fields: []field{
		{"ITEM_TYPE", "i32", 2294}, {"ITEM_STACK", "i32", 1},
		{"ITEM_FISHING_POLE", "u8", 25},
	}},
	{slot: 3, fields: []field{
		{"ITEM_TYPE", "i32", 4}, {"ITEM_STACK", "i32", 1},
		{"ITEM_DAMAGE", "i32", 12}, {"ITEM_DEFENSE", "i32", 0},
		{"ITEM_PREFIX", "u8", 81}, {"ITEM_MELEE", "u8", 1},
	}},
	{slot: 4, fields: []field{ // a second pickaxe, for the mining sweep
		{"ITEM_TYPE", "i32", 3503}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 35}, {"ITEM_USE_TIME", "i32", 23},
		{"ITEM_USE_ANIM", "i32", 23},
	}},
	{slot: 5},               // an object whose type is zero
	{slot: 6, absent: true}, // no object at all
}

/*
snapItemsImage is the inert copy's inventory: the starting items, which is what
a load-time snapshot holds.

Deliberately different from the live one, and *larger* in one respect -- it has
a pickaxe the live player no longer carries. A reader that fell back to "the
copy with the most items" would pick this one, and a sweep that reported
whichever copy it wrote last would report these slots.
*/
var snapItemsImage = []item{
	{slot: 0, fields: []field{
		{"ITEM_TYPE", "i32", 3509}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 35}, {"ITEM_USE_TIME", "i32", 23},
		{"ITEM_USE_ANIM", "i32", 23},
	}},
	{slot: 1, fields: []field{
		{"ITEM_TYPE", "i32", 3506}, {"ITEM_STACK", "i32", 1},
		{"ITEM_AXE", "i32", 35},
	}},
	{slot: 2, fields: []field{
		{"ITEM_TYPE", "i32", 3505}, {"ITEM_STACK", "i32", 1},
		{"ITEM_HAMMER", "i32", 35},
	}},
	{slot: 3, fields: []field{
		{"ITEM_TYPE", "i32", 3509}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 35},
	}},
	{slot: 7, fields: []field{
		{"ITEM_TYPE", "i32", 9}, {"ITEM_STACK", "i32", 99},
	}},
}

// execMem is a fake whose whole buffer is also code, so the pattern search has
// somewhere to look.
type execMem struct{ *memtest.FakeMem }

/*
ExePath is nothing: a planted game has no file behind it.

That is the honest answer and it exercises a real state -- the game can be run
from outside Steam, where there is no manifest to read and the build gate has to
carry on without one.
*/
func (m *execMem) ExePath() string { return "" }

// plant writes the whole image into a Go fake.
func plant() *execMem {
	mem := memtest.New(base, size)
	mem.Exec = []proc.Region{{Start: code, End: code + 0x100}}

	mem.PlantMonoString(snapName, "Nakama")
	mem.PlantMonoString(liveName, "Nakama")
	mem.PlantPlayer(snapLife, snapBlock, snapName)
	mem.PlantPlayer(snap2Life, snap2Block, snapName)
	mem.PlantPlayer(liveLife, liveBlock, liveName)

	// get_LocalPlayer: mov eax,[Main.player]; mov ecx,[Main.myPlayer]; tail.
	asm := append([]byte{0x8B, 0x05}, u32(playerStatic)...)
	asm = append(asm, 0x8B, 0x0D)
	asm = append(asm, u32(myPlayerStatic)...)
	asm = append(asm, localPlayerTail...)
	mem.PokeBytes(code, asm)
	mem.PokeBytes(playerStatic, u32(playerArray))
	mem.PokeI32(myPlayerStatic, myPlayer)
	mem.PokeBytes(playerArray+layout.ArrDataOff+myPlayer*4, u32(liveObj))

	plantInventory(mem, liveLife, liveArr, liveItems, liveItemsImage)
	plantInventory(mem, snapLife, snapArr, snapItems, snapItemsImage)
	plantInventory(mem, snap2Life, snap2Arr, snap2Items, snap2ItemsImage)
	return &execMem{mem}
}

// plantInventory writes one copy's Item[] and the objects in it.
func plantInventory(mem *memtest.FakeMem, life, arr, items uint32, image []item) {
	mem.PokeBytes(uint32(int(life)+layout.InventoryPtrOff), u32(arr)) //nolint:gosec // a planted address
	for _, it := range image {
		if it.absent {
			continue
		}
		addr := items + uint32(it.slot)*itemStride                        //nolint:gosec // a slot index
		mem.PokeBytes(arr+layout.ArrDataOff+uint32(it.slot)*4, u32(addr)) //nolint:gosec // a slot index
		for _, f := range it.fields {
			off := uint32(layout.Offsets[f.name]) //nolint:gosec // an offset from the table
			switch f.kind {
			case "u8":
				mem.PokeBytes(addr+off, []byte{byte(f.value)})
			case "f32":
				mem.WriteF32(addr+off, float32(f.value))
			default:
				mem.PokeI32(addr+off, int32(f.value))
			}
		}
	}
}

// localPlayerTail is the pattern the resolver looks for, which the locate
// package's own tests pin against the Python.
var localPlayerTail = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// liveIs is what the fixture says the live player's address is, so a test can
// say which copy it expected rather than only that the two agreed.
const liveIs = uint32(liveLife)

var _ = locate.StatLifeFromObj // the fixture's liveLife is built from it

/*
preamble is the same image as Python source, planted with the Python's own
constants and its own fake.

Generated from the tables above so the two images cannot drift, but every offset
in it is looked up by name on the Python side.
*/
func preamble() string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import inventory as I, locate as L
from terrariabonker.service import Service
mem = FakeMem(%d, %d)
def u8(a, v): mem.poke_bytes(a, bytes([v]))
def f32(a, v): mem.write_f32(a, v)
def i32(a, v): mem.poke_i32(a, v)
mem.plant_mono_string(%d, "Nakama")
mem.plant_mono_string(%d, "Nakama")
mem.plant_player(%d, %v, %d)
mem.plant_player(%d, %v, %d)
mem.plant_player(%d, %v, %d)
code = (b"\x8b\x05" + struct.pack("<I", %d) + b"\x8b\x0d" + struct.pack("<I", %d)
        + L._LOCALPLAYER_TAIL)
mem.poke_bytes(%d, code)
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d, %d)
mem.poke_bytes(%d + I.ARR_DATA_OFF + %d * 4, struct.pack("<I", %d))
L._exec_regions = lambda m: [(%d, %d)]
`,
		base, size, snapName, liveName,
		snapLife, ints(snapBlock), snapName,
		snap2Life, ints(snap2Block), snapName,
		liveLife, ints(liveBlock), liveName,
		playerStatic, myPlayerStatic, code,
		playerStatic, playerArray, myPlayerStatic, myPlayer,
		playerArray, myPlayer, liveObj,
		code, code+0x100)

	pythonInventory(&b, liveLife, liveArr, liveItems, liveItemsImage)
	pythonInventory(&b, snapLife, snapArr, snapItems, snapItemsImage)
	pythonInventory(&b, snap2Life, snap2Arr, snap2Items, snap2ItemsImage)
	fmt.Fprintf(&b, "svc = Service(mem)\n")
	return b.String()
}

// pythonInventory is one copy's inventory as Python source.
func pythonInventory(b *strings.Builder, life, arr, items uint32, image []item) {
	fmt.Fprintf(b, "mem.poke_i32(%d + I.INVENTORY_PTR_OFF, %d)\n", life, arr)
	for _, it := range image {
		if it.absent {
			continue
		}
		addr := items + uint32(it.slot)*itemStride //nolint:gosec // a slot index
		fmt.Fprintf(b, "mem.poke_i32(%d + I.ARR_DATA_OFF + %d * 4, %d)\n", arr, it.slot, addr)
		for _, f := range it.fields {
			fmt.Fprintf(b, "%s(%d + I.%s, %v)\n", f.kind, addr, f.name, f.value)
		}
	}
}

// ints is a block as a Python list.
func ints(b []int32) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// layoutArrData is where an array's elements start, from the same table the
// planting uses.
var layoutArrData = uint32(layout.Offsets["ARR_DATA_OFF"]) //nolint:gosec // a small offset

// plantNothing is a game with nothing loaded: mapped memory, no player, no
// get_LocalPlayer.
func plantNothing() *execMem {
	return &execMem{memtest.New(base, 0x4000)}
}
