package service_test

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
	"github.com/ushineko/terrariabonker/internal/selling"
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
	size = 0x100000 // wide enough to hold the world, the templates, the NPCs and an arena

	// The two inert snapshots, and the live player behind get_LocalPlayer.
	snapLife  = base + 0x3000
	snap2Life = base + 0x2A000
	liveObj   = base + 0x8000
	liveLife  = liveObj + 0x738 // locate.StatLifeFromObj

	snapName = base + 0x40
	liveName = base + 0x80

	/*
		get_LocalPlayer and the two statics it reads.

		Main.player's static sits at its own offset inside Main's block, because
		that is how the base is derived: the resolver finds this address and
		subtracts the offset. Putting it anywhere else would leave the derived
		base pointing at nothing, and the world would read as unloaded however
		carefully it was planted.
	*/
	code           = base + 0x1000
	playerStatic   = staticAt + 0xA7C // layout.MainPlayerOff
	myPlayerStatic = playerStatic + 4
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

	// The vtable every item object is behind, planted so the template search has
	// something to recognise.
	itemVTable = 0xDEADBEEF

	// Where the executable part of the fixture ends, which is everything the
	// scans look at and nothing this program allocated.
	codeEnd = base + 0x58000
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

/*
AllRegions is the one mapping this fake has, read-write-execute.

The patcher wants the whole listing rather than the scannable part: it is looking
for somewhere to put an arena and for what is already taken, and a mapping
nobody scans still occupies its addresses.
*/
func (m *execMem) AllRegions() []proc.Region {
	return []proc.Region{{
		Start: base, End: base + size,
		Readable: true, Writable: true, Executable: true,
	}}
}

// plant writes the whole image into a Go fake.
func plant() *execMem {
	mem := memtest.New(base, size)
	/*
		The whole buffer is executable, so a patch anchor can be planted anywhere
		in it.

		The pattern search reads only what a listing says is code, and with a
		hundred-byte window there is nowhere to put an anchor but on top of
		something else.
	*/
	/*
		The code the scans look at: everything up to the arena, and not the arena
		itself.

		A scanner excludes the region holding this program's own arena, because
		memory it put something in is never padding -- and it excludes the whole
		region. With one mapping covering the arena too, that skip swallowed
		everything and no anchor could be found at all.
	*/
	mem.Exec = []proc.Region{{Start: base, End: codeEnd, Executable: true}}

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
		/*
			Every item object carries the class's vtable, as the game's do. It is
			what the template search matches on: without it there is nothing to
			tell an item object from any other four bytes that happen to hold an
			item type.
		*/
		mem.PokeBytes(addr, u32(itemVTable))
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
		fmt.Fprintf(b, "mem.poke_bytes(%d, struct.pack(\"<I\", %d))\n", addr, itemVTable)
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

/*
plantWorldInto puts a world in the fixture: a name, a tile buffer and the
dimensions.

Every world the tests plant is the same size, so the name is the only thing
telling them apart -- which is the point.
*/
func plantWorldInto(mem *execMem, name string) { plantWorldAt(mem, staticAt, name) }

/*
plantWorldAt is the same at a chosen static base, plus the statics that lead
there.

A world reload moves Main's block, so recovering means finding it again through
get_LocalPlayer -- which is why this repoints that too.
*/
func plantWorldAt(mem *execMem, at uint32, name string) {
	staticAt := at
	tileBufAt, tileBoundsAt := at+0x1000, at+0x2000
	worldNameAt := at + 0x3000
	mem.PokeBytes(staticAt+0xA7C, u32(playerArray)) // Main.player, which the base is derived from
	mem.PokeBytes(code+2, u32(staticAt+0xA7C))      // and what get_LocalPlayer reads

	mem.PokeBytes(staticAt+layout.MainTileOff, u32(tileBufAt))
	mem.PokeI32(staticAt+layout.MainMaxTilesOff, worldWidth)
	mem.PokeI32(staticAt+layout.MainMaxTilesOff+4, worldHeight)
	mem.PokeBytes(tileBufAt+0x08, u32(tileBoundsAt))
	mem.PokeI32(tileBoundsAt+0x04, 0)
	mem.PokeI32(tileBoundsAt+0x08, worldHeight)
	mem.PokeI32(tileBoundsAt+0x0C, 0)

	mem.PlantMonoString(worldNameAt, name)
	mem.PokeBytes(staticAt+layout.MainWorldNameOff, u32(worldNameAt))
}

// movedStaticAt is where a world reload puts Main's block the second time.
const movedStaticAt = base + 0x20000

/*
plantWorld is the same, as Python source.

The static base is fixed rather than scanned for: what is being compared is what
the two make of a world, not how each finds Main.
*/
func plantWorld(name string) string {
	return fmt.Sprintf(`
from terrariabonker import layout as L, locate as LC
mem.poke_bytes(%d + L.MAIN_TILE_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + L.MAIN_MAX_TILES_OFF, %d)
mem.poke_i32(%d + L.MAIN_MAX_TILES_OFF + 4, %d)
mem.poke_bytes(%d + 0x08, struct.pack("<I", %d))
mem.poke_i32(%d + 0x04, 0)
mem.poke_i32(%d + 0x08, %d)
mem.poke_i32(%d + 0x0C, 0)
mem.plant_mono_string(%d, %q)
mem.poke_bytes(%d + L.MAIN_WORLD_NAME_OFF, struct.pack("<I", %d))
LC.main_static_base = lambda m: %d
svc._main_base = %d
`, staticAt, tileBufAt, staticAt, worldWidth, staticAt, worldHeight,
		tileBufAt, tileBoundsAt, tileBoundsAt, tileBoundsAt, worldHeight, tileBoundsAt,
		worldNameAt, name, staticAt, worldNameAt, staticAt, staticAt)
}

// Where the world goes in the planted game.
const (
	staticAt     = base + 0x10000
	tileBufAt    = base + 0x11000
	tileBoundsAt = base + 0x12000
	worldNameAt  = base + 0x13000
	/*
		A small world, because the fixture's memory has to hold it.

		The buffer is one pointer per tile and the objects are twenty-four bytes
		each, so a real world's four million tiles would index a hundred megabytes
		past the end of a planted one. Both implementations read nothing there and
		agree about it perfectly, which is why the tests about a vein assert its
		size rather than only that the two matched.
	*/
	worldWidth  = 40
	worldHeight = 60
)

/*
countingMem records how many times the executable mappings were listed.

Listing them is the first step of the scan that finds Main's statics, and the
scan is the expensive thing -- over a second against a real process. Counting
reads would say nothing here, because a planted game's code is a few hundred
bytes; counting the scans says exactly what the caching is for.
*/
type countingMem struct {
	*execMem
	scans int
}

func (c *countingMem) ExecRegions() []proc.Region {
	c.scans++
	return c.execMem.ExecRegions()
}

// layoutArrData is where an array's elements start, from the same table the
// planting uses.
var layoutArrData = uint32(layout.Offsets["ARR_DATA_OFF"]) //nolint:gosec // a small offset

// plantNothing is a game with nothing loaded: mapped memory, no player, no
// get_LocalPlayer.
func plantNothing() *execMem {
	return &execMem{memtest.New(base, 0x4000)}
}

// The item offsets the fixture plants with, from the one table that declares
// them.
var (
	layoutItemType      = int(layout.Offsets["ITEM_TYPE"])
	layoutItemDamage    = int(layout.Offsets["ITEM_DAMAGE"])
	layoutItemUseTime   = int(layout.Offsets["ITEM_USE_TIME"])
	layoutItemUseAnim   = int(layout.Offsets["ITEM_USE_ANIM"])
	layoutItemRare      = int(layout.Offsets["ITEM_RARE"])
	layoutItemKnockback = int(layout.Offsets["ITEM_KNOCKBACK"])
	layoutItemScale     = int(layout.Offsets["ITEM_SCALE"])
)

// itemAddr is where the live player's item for a slot was planted.
func itemAddr(slot int) uint32 {
	return liveItems + uint32(slot)*itemStride //nolint:gosec // a slot index
}

/*
sameMemory compares two whole memory images and, when they differ, says where.

Printing the difference between two images this size is useless -- hundreds of
kilobytes of hex, which the test runner will not even display. The address of the
first byte that differs and a few either side is what a person needs: it names
the slot, the copy or the field that went wrong.
*/
func sameMemory(t *testing.T, want, got, msg string) {
	t.Helper()
	if want == got {
		return
	}
	require.Equalf(t, len(want), len(got), "%s: the images are different sizes", msg)

	// Two hex characters per byte, so the byte is the character index halved.
	at := 0
	for at < len(want) && want[at] == got[at] {
		at++
	}
	lo, hi := max(0, at/2-8), min(len(want)/2, at/2+16)
	require.Failf(t, msg, "first difference at %#x\n  want % s\n  got  % s",
		base+at/2, want[lo*2:hi*2], got[lo*2:hi*2])
}

/*
Where the world's tile objects go, and how one is planted.

The buffer is column-major, so the entry for a coordinate is at
stride*x + y and the objects sit contiguously down each column -- which is what
the whole-world search depends on.
*/
const (
	tileObjectsAt = base + 0x30000
	tileRecord    = 24
)

// plantTile puts one tile in the planted world: its id, and whether it is really
// there.
func plantTile(mem *execMem, x, y int32, id uint16, active bool) {
	idx := worldHeight*x + y
	at := uint32(tileObjectsAt + uint32(idx)*tileRecord)              //nolint:gosec // a planted address
	mem.PokeBytes(tileBufAt+layout.ArrDataOff+uint32(idx)*4, u32(at)) //nolint:gosec // an index
	mem.PokeBytes(at+0x08, []byte{byte(id), byte(id >> 8)})
	var header uint16
	if active {
		header = 0x20
	}
	mem.PokeBytes(at+0x0E, []byte{byte(header), byte(header >> 8)})
}

// pyTile is the same, as Python source.
func pyTile(x, y int32, id uint16, active bool) string {
	idx := worldHeight*x + y
	at := tileObjectsAt + uint32(idx)*tileRecord //nolint:gosec // a planted address
	header := 0
	if active {
		header = 0x20
	}
	return fmt.Sprintf(`
mem.poke_bytes(%d + L.ARR_DATA_OFF + %d * 4, struct.pack("<I", %d))
mem.poke_bytes(%d + 0x08, struct.pack("<H", %d))
mem.poke_bytes(%d + 0x0E, struct.pack("<H", %d))
`, tileBufAt, idx, at, at, id, at, header)
}

// plantPositionInto puts the player somewhere in the world, in world pixels.
func plantPositionInto(mem *execMem, px, py float32) {
	obj := uint32(liveLife) - 0x738
	mem.WriteF32(obj+0x0C, px)
	mem.WriteF32(obj+0x10, py)
}

// plantPosition is the same, as Python source.
func plantPosition(px, py float32) string {
	return fmt.Sprintf(`
mem.write_f32(%d + 0x0C, %v)
mem.write_f32(%d + 0x10, %v)
`, liveLife-0x738, px, liveLife-0x738, py)
}

/*
patchFor is a patcher over the planted game, with its record kept somewhere
disposable.

enabledPatcher is the same with the extractor applied, for the tests about what
happens once it is.
*/
func patchFor(t *testing.T, mem *execMem) *patch.Patcher {
	t.Helper()
	atHome(t)
	return patch.NewPatcher(mem, -1)
}

/*
enabledPatcher is a patcher over a game the extractor is really applied to.

A record alone is not evidence and is not treated as any: whether a cheat is on
is answered by reading the bytes at its site. So the anchor is planted and a jump
written over its injection point, which is what an applied cheat looks like.
*/
func enabledPatcher(t *testing.T, mem *execMem) *patch.Patcher {
	t.Helper()
	inj := patch.Injections["ore_extract"]
	anchor := patch.Anchors[inj.Anchor].Pattern

	body := make([]byte, anchor.Len())
	for i := range body {
		body[i] = 0xCC
		if anchor.Mask[i] {
			body[i] = anchor.Raw[i]
		}
	}
	mem.PokeBytes(anchorAt, body)
	mem.PokeBytes(uint32(int64(anchorAt)+int64(inj.InjectOff)), []byte{0xE9, 0, 0, 0, 0})
	return patchFor(t, mem)
}

/*
anchorAt is where a patch anchor is planted.

Clear of the tile objects, which run from 0x30000 for twenty-four bytes per tile
of the world -- it sat inside them at first, and planting the world wrote over
the anchor, so the cheat read as off however carefully it was applied.
*/
const anchorAt = base + 0x50000

// atHome points the patch record at a scratch directory.
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

/*
miningMem is a planted game that actually mines.

Nothing in a fake breaks a tile, so the loop that hands a vein over a batch at a
time -- and re-finds it between batches -- had nothing to walk. This watches for
the queue's count being written, which is the last thing an arm does, and clears
exactly the tiles it names. That is what the stub does in the game.
*/
type miningMem struct {
	*execMem
	arena uint32
	// falls makes the tiles drop by one row when they are taken, as silt and
	// slush do, so the re-find between batches has something to re-find.
	falls bool
	armed int
	/*
		pending is the batch the stub has been handed but has not run yet.

		The stub runs on the game's next frame, not when the queue is written, so
		an instant fake would let a caller that never waited look correct. This
		one takes a batch when it is armed and breaks it on the next look at the
		world.
	*/
	pending []patch.Tile
	// waits is how many looks at the world go by before the batch is broken,
	// standing in for the frames the stub waits for.
	waits int
	// deaf is a game that takes a queue and never runs it, which is what a
	// paused one looks like.
	deaf bool
}

// framesToMine is how many reads a batch takes to break.
//
// More than one on purpose. With a fake that mines on the next read, a caller
// that armed and counted immediately would see everything gone and look
// correct -- the wait it is supposed to do would be testing nothing.
const framesToMine = 4

func (m *miningMem) AllRegions() []proc.Region {
	return append(m.execMem.AllRegions(), proc.Region{
		Start: m.arena, End: m.arena + patch.ArenaSize,
		Readable: true, Writable: true, Executable: true,
	})
}

func (m *miningMem) Write(addr uint32, data []byte) bool {
	ok := m.execMem.Write(addr, data)
	if addr != m.arena+patch.OreQueueOff || len(data) != 4 {
		return ok
	}
	n := int32(binary.LittleEndian.Uint32(data)) //nolint:gosec // a count, as its bits
	if n <= 0 {
		return ok
	}
	m.armed++
	pairs := m.execMem.Read(m.arena+patch.OreQueueOff+4, int(n)*8)
	m.pending, m.waits = nil, framesToMine
	for i := range int(n) {
		m.pending = append(m.pending, patch.Tile{
			X: int32(binary.LittleEndian.Uint32(pairs[i*8:])),   //nolint:gosec // a coordinate
			Y: int32(binary.LittleEndian.Uint32(pairs[i*8+4:])), //nolint:gosec // a coordinate
		})
	}
	return ok
}

/*
Read runs the pending batch before answering.

The stub runs on the game's next frame rather than when the queue is written, so
a caller that armed and looked immediately would see nothing gone. Breaking the
batch on the next look is the closest a fake gets to that, and it is what makes
"wait for the tiles to be gone" a rule a test can hold this to.
*/
func (m *miningMem) Read(addr uint32, size int) []byte {
	if m.waits > 0 {
		m.waits--
	}
	if m.pending != nil && m.waits == 0 && !m.deaf {
		batch := m.pending
		m.pending = nil
		for _, q := range batch {
			id, _ := readTile(m.execMem, q.X, q.Y)
			plantTile(m.execMem, q.X, q.Y, 0, false)
			if m.falls {
				// It did not vanish: it fell one row, onto whatever is below.
				plantTile(m.execMem, q.X, q.Y+1, id, true)
			}
		}
	}
	return m.execMem.Read(addr, size)
}

// readTile is one planted tile's id.
func readTile(mem *execMem, x, y int32) (uint16, bool) {
	idx := worldHeight*x + y
	at := uint32(tileObjectsAt + uint32(idx)*tileRecord) //nolint:gosec // a planted address
	raw := mem.Read(at+0x08, 2)
	if len(raw) < 2 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(raw), true
}

/*
miningGame is a planted game with an arena, so the queue can be armed, and a
stub that mines what lands in it.
*/
func miningGame(t *testing.T, falls bool) (*miningMem, *patch.Patcher) {
	t.Helper()
	mem := plant()
	plantWorldInto(mem, "Nakama's World")

	const arena = base + 0x60000
	mem.PokeBytes(arena+patch.ArenaMagicOff, patch.ArenaMagic)
	game := &miningMem{execMem: mem, arena: arena, falls: falls}

	inj := patch.Injections["ore_extract"]
	anchor := patch.Anchors[inj.Anchor].Pattern
	body := make([]byte, anchor.Len())
	for i := range body {
		body[i] = 0xCC
		if anchor.Mask[i] {
			body[i] = anchor.Raw[i]
		}
	}
	mem.PokeBytes(anchorAt, body)
	mem.PokeBytes(uint32(int64(anchorAt)+int64(inj.InjectOff)), []byte{0xE9, 0, 0, 0, 0})

	atHome(t)
	return game, patch.NewPatcher(game, -1)
}

// pyProfileAt points the Python's profile at a scratch file, so a test never
// touches the maintainer's own.
func pyProfileAt(home string) string {
	return fmt.Sprintf(`
import os
from terrariabonker import profile
profile._PATH = os.path.join(%q, ".config", "terrariabonker", "profile.json")
`, home)
}

// Where the player's buff arrays go, and what is already running.
const (
	buffTypeArr = base + 0x34000
	buffTimeArr = base + 0x35000
	buffSlots   = 44
)

/*
plantBuffsInto gives the player a bar with one of the fishing effects already
running for eight minutes, which is what drinking a potion looks like.
*/
func plantBuffsInto(mem *execMem) {
	mem.PokeBytes(uint32(int64(liveLife)+layout.BuffTypePtrOff), u32(buffTypeArr)) //nolint:gosec // a delta
	mem.PokeBytes(uint32(int64(liveLife)+layout.BuffTimePtrOff), u32(buffTimeArr)) //nolint:gosec // a delta
	mem.PokeI32(buffTypeArr+layout.ArrLenOff, buffSlots)
	mem.PokeI32(buffTimeArr+layout.ArrLenOff, buffSlots)
	mem.PokeI32(buffTypeArr+layout.ArrDataOff, 121)
	mem.PokeI32(buffTimeArr+layout.ArrDataOff, 28800)
}

// plantBuffs is the same, as Python source.
func plantBuffs() string {
	return fmt.Sprintf(`
from terrariabonker import buffs as BF, layout as L2
mem.poke_bytes(%d + BF.BUFF_TYPE_PTR_OFF, struct.pack("<I", %d))
mem.poke_bytes(%d + BF.BUFF_TIME_PTR_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + L2.ARR_LEN_OFF, %d)
mem.poke_i32(%d + L2.ARR_LEN_OFF, %d)
mem.poke_i32(%d + L2.ARR_DATA_OFF, 121)
mem.poke_i32(%d + L2.ARR_DATA_OFF, 28800)
`, liveLife, buffTypeArr, liveLife, buffTimeArr,
		buffTypeArr, buffSlots, buffTimeArr, buffSlots, buffTypeArr, buffTimeArr)
}

// writeWatcher counts writes, for the tests about what happens before what.
type writeWatcher struct {
	*execMem
	writes int
}

func (w *writeWatcher) Write(addr uint32, data []byte) bool {
	w.writes++
	return w.execMem.Write(addr, data)
}

/*
plantBaitInto gives the player a bait stack below any sensible floor.

The base fixture carries none, which made every question about topping bait up a
question with no bait in it.
*/
func plantBaitInto(mem *execMem) {
	const slot, at = 7, liveItems + 7*itemStride
	mem.PokeBytes(liveArr+layout.ArrDataOff+slot*4, u32(at))
	mem.PokeBytes(at, u32(itemVTable))
	mem.PokeI32(at+uint32(layout.ItemType), 2675)         //nolint:gosec // a field offset
	mem.PokeI32(at+uint32(layout.ItemStack), 12)          //nolint:gosec // a field offset
	mem.PokeBytes(at+uint32(layout.ItemBait), []byte{15}) //nolint:gosec // a field offset
}

// pyPlantBait is the same, as Python source.
func pyPlantBait() string {
	const slot, at = 7, liveItems + 7*itemStride
	return fmt.Sprintf(`
mem.poke_bytes(%d + I.ARR_DATA_OFF + %d * 4, struct.pack("<I", %d))
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + I.ITEM_TYPE, 2675)
mem.poke_i32(%d + I.ITEM_STACK, 12)
mem.poke_bytes(%d + I.ITEM_BAIT, bytes([15]))
`, liveArr, slot, at, at, itemVTable, at, at, at)
}

// Where a second rod goes, and what it was carrying.
const (
	secondRodSlot = 8
	secondRodAt   = liveItems + secondRodSlot*itemStride
)

// plantSecondRod gives the player a rod the cheat has never touched.
func plantSecondRod(mem *execMem) {
	mem.PokeBytes(liveArr+layout.ArrDataOff+secondRodSlot*4, u32(secondRodAt))
	mem.PokeBytes(secondRodAt, u32(itemVTable))
	mem.PokeI32(secondRodAt+uint32(layout.ItemType), 2289)                //nolint:gosec // a field offset
	mem.PokeI32(secondRodAt+uint32(layout.ItemStack), 1)                  //nolint:gosec // a field offset
	mem.PokeBytes(secondRodAt+uint32(layout.ItemFishingPole), []byte{40}) //nolint:gosec // a field offset
}

// secondRodPower is what that rod's fishing power reads now.
func secondRodPower(mem *execMem) byte {
	raw := mem.Read(secondRodAt+uint32(layout.ItemFishingPole), 1) //nolint:gosec // a field offset
	if len(raw) < 1 {
		return 0
	}
	return raw[0]
}

// Where the projectile array and its objects go.
const (
	projArrAt     = base + 0x38000
	projObjectsAt = base + 0x3A000
	projArrayLen  = 1001
)

// bobber is one projectile planted in the array.
type bobber struct {
	slot    int
	ptype   int32
	ai      [3]float32
	localAI [3]float32
}

/*
plantProjectilesInto fills the array and puts the named projectiles in it.

Every slot is allocated behind one vtable, as the game allocates them: that
agreement is what tells the array from anything else of the same length.
*/
func plantProjectilesInto(mem *execMem, live []bobber) {
	mem.PokeBytes(staticAt+layout.MainProjectileOff, u32(projArrAt))
	mem.PokeI32(projArrAt+layout.ArrLenOff, projArrayLen)
	for i := range projArrayLen {
		obj := uint32(projObjectsAt + i*0x30) //nolint:gosec // a planted address
		mem.PokeBytes(projArrAt+layout.ArrDataOff+uint32(i)*4, u32(obj))
		mem.PokeBytes(obj, u32(itemVTable))
		// Cleared, so a slot left over from an earlier planting is not still a
		// live bobber.
		mem.PokeBytes(obj+uint32(layout.ProjectileActive), []byte{0})
		mem.PokeBytes(obj+uint32(layout.ProjectileBobber), []byte{0})
	}
	for i, b := range live {
		obj := uint32(projObjectsAt + b.slot*0x30) //nolint:gosec // a planted address
		mem.PokeBytes(obj+uint32(layout.ProjectileActive), []byte{1})
		mem.PokeI32(obj+uint32(layout.ProjectileType), b.ptype)
		if b.ptype == 0 {
			mem.PokeBytes(obj+uint32(layout.ProjectileBobber), []byte{1})
		}
		ai := uint32(projFloatsAt + i*0x40)           //nolint:gosec // a planted address
		localAI := uint32(projFloatsAt + i*0x40 + 32) //nolint:gosec // a planted address
		mem.PokeBytes(obj+uint32(layout.ProjectileAI), u32(ai))
		mem.PokeBytes(obj+uint32(layout.ProjectileLocalAI), u32(localAI))
		for j := range 3 {
			mem.WriteF32(ai+layout.ArrDataOff+uint32(j)*4, b.ai[j])
			mem.WriteF32(localAI+layout.ArrDataOff+uint32(j)*4, b.localAI[j])
		}
	}
}

// projFloatsAt is where the projectiles' float arrays go.
const projFloatsAt = base + 0x3F000

/*
autoUsePatcher is a patcher over a game auto-use is really applied to, with an
arena so its words can be armed.

Whether a cheat is on is answered by reading the bytes at its site, so the anchor
is planted and a jump written over its injection point.
*/
func autoUsePatcher(t *testing.T, mem *execMem) *patch.Patcher {
	t.Helper()
	inj := patch.Injections["auto_use"]
	anchor := patch.Anchors[inj.Anchor].Pattern
	body := make([]byte, anchor.Len())
	for i := range body {
		body[i] = 0xCC
		if anchor.Mask[i] {
			body[i] = anchor.Raw[i]
		}
	}
	mem.PokeBytes(autoUseAnchorAt, body)
	mem.PokeBytes(uint32(int64(autoUseAnchorAt)+int64(inj.InjectOff)), []byte{0xE9, 0, 0, 0, 0})
	mem.PokeBytes(arenaAt+patch.ArenaMagicOff, patch.ArenaMagic)

	atHome(t)
	return patch.NewPatcher(&arenaMem{execMem: mem}, -1)
}

// Where auto-use's anchor and this program's arena go in the planted game.
const (
	autoUseAnchorAt = base + 0x52000
	arenaAt         = base + 0x60000
)

// arenaMem is a planted game with an arena mapping beside its code.
type arenaMem struct{ *execMem }

func (m *arenaMem) AllRegions() []proc.Region {
	return append(m.execMem.AllRegions(), proc.Region{
		Start: arenaAt, End: arenaAt + patch.ArenaSize,
		Readable: true, Writable: true, Executable: true,
	})
}

/*
plantHoldingRod puts the rod in the player's hand.

Casting checks that, because the use button is not fishing-specific: pressing it
against a sword swings the sword. The base fixture leaves the held slot at zero,
which is a pickaxe.
*/
func plantHoldingRod(mem *execMem) {
	const rodSlot = 2                                                    // where the fixture's live player keeps their rod
	mem.PokeI32(uint32(int64(liveLife)+layout.SelectedItemOff), rodSlot) //nolint:gosec // a delta
}

// plantHoldingSlot puts a chosen hotbar slot in the player's hand.
func plantHoldingSlot(mem *execMem, slot int32) {
	mem.PokeI32(uint32(int64(liveLife)+layout.SelectedItemOff), slot) //nolint:gosec // a delta
}

/*
plantFavorite marks one of the live player's slots as favorited, and makes it a
potion worth carrying.

The favourite is the player's opt-in: without it every potion picked up would
start doing something.
*/
func plantFavorite(mem *execMem, slot int) {
	at := liveItems + uint32(slot)*itemStride                  //nolint:gosec // a slot index
	mem.PokeBytes(at+uint32(layout.ItemFavorited), []byte{1})  //nolint:gosec // a field offset
	mem.PokeBytes(at+uint32(layout.ItemConsumable), []byte{1}) //nolint:gosec // a field offset
	mem.PokeI32(at+uint32(layout.ItemBuffType), 122)           //nolint:gosec // a field offset
}

// itoa and sprintf keep the tests readable.
func itoa(v int) string                 { return fmt.Sprintf("%d", v) }
func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

// Where a sellable stack and the bank go in the planted game.
const (
	sellableSlot = 9
	sellableAt   = liveItems + sellableSlot*itemStride
	bankChestAt  = base + 0x44000
	bankArrAt    = base + 0x45000
	bankItemsAt  = base + 0x46000
	carriedSlot  = 10
	carriedAt    = liveItems + carriedSlot*itemStride
)

// plantSellableInto gives the player a stack worth selling.
func plantSellableInto(mem *execMem) {
	mem.PokeBytes(liveArr+layout.ArrDataOff+sellableSlot*4, u32(sellableAt))
	mem.PokeBytes(sellableAt, u32(itemVTable))
	mem.PokeI32(sellableAt+uint32(layout.ItemType), 9)   //nolint:gosec // a field offset
	mem.PokeI32(sellableAt+uint32(layout.ItemStack), 99) //nolint:gosec // a field offset
	mem.PokeI32(sellableAt+layout.ItemValue, 500)
}

// plantFavoriteOnly marks a slot favorited without making it a potion.
func plantFavoriteOnly(mem *execMem, slot int) {
	at := liveItems + uint32(slot)*itemStride                 //nolint:gosec // a slot index
	mem.PokeBytes(at+uint32(layout.ItemFavorited), []byte{1}) //nolint:gosec // a field offset
}

// plantCarriedBank gives the player a piggy bank to carry.
func plantCarriedBank(mem *execMem) {
	mem.PokeBytes(liveArr+layout.ArrDataOff+carriedSlot*4, u32(carriedAt))
	mem.PokeBytes(carriedAt, u32(itemVTable))
	mem.PokeI32(carriedAt+uint32(layout.ItemType), selling.PiggyBankItem) //nolint:gosec // a field offset
	mem.PokeI32(carriedAt+uint32(layout.ItemStack), 1)                    //nolint:gosec // a field offset
}

// plantBankInto gives the player a bank chest with room in it.
func plantBankInto(mem *execMem) {
	plantCarriedBank(mem)
	mem.PokeBytes(uint32(int64(liveLife)+layout.BankPtrOff), u32(bankChestAt)) //nolint:gosec // a delta
	mem.PokeBytes(bankChestAt+layout.ChestItemOff, u32(bankArrAt))
	mem.PokeI32(bankArrAt+layout.ArrLenOff, layout.BankSlots)
	for i := range layout.BankSlots {
		at := uint32(bankItemsAt + uint32(i)*0x200) //nolint:gosec // a slot index
		mem.PokeBytes(bankArrAt+layout.ArrDataOff+uint32(i)*4, u32(at))
		mem.PokeBytes(at, u32(itemVTable))
	}
}

/*
fillEverySlot leaves the player nowhere to put a coin.

Every slot the sell path may pay into holds something that is not a coin and is
not on the list, which is what "no room" looks like.
*/
func fillEverySlot(mem *execMem) {
	for i := range layout.CoinSlots {
		if i == sellableSlot {
			continue
		}
		at := uint32(liveItems + uint32(i)*itemStride) //nolint:gosec // a slot index
		mem.PokeBytes(liveArr+layout.ArrDataOff+uint32(i)*4, u32(at))
		mem.PokeBytes(at, u32(itemVTable))
		mem.PokeI32(at+uint32(layout.ItemType), 3507) //nolint:gosec // a field offset
		mem.PokeI32(at+uint32(layout.ItemStack), 1)   //nolint:gosec // a field offset
	}
}

// readCounter records how many times the game was read, for the tests about
// what is asked once rather than every round.
type readCounter struct {
	*execMem
	reads int
}

func (c *readCounter) Read(addr uint32, size int) []byte {
	c.reads++
	return c.execMem.Read(addr, size)
}

// plantBankCoins puts a stack of one denomination in a bank slot.
func plantBankCoins(mem *execMem, slot int, coin, stack int32) {
	at := uint32(bankItemsAt + uint32(slot)*0x200)  //nolint:gosec // a slot index
	mem.PokeI32(at+uint32(layout.ItemType), coin)   //nolint:gosec // a field offset
	mem.PokeI32(at+uint32(layout.ItemStack), stack) //nolint:gosec // a field offset
}

// plantSellableWorth gives the player a stack worth a chosen amount.
func plantSellableWorth(mem *execMem, value, stack int32) {
	plantSellableInto(mem)
	mem.PokeI32(sellableAt+layout.ItemValue, value)
	mem.PokeI32(sellableAt+uint32(layout.ItemStack), stack) //nolint:gosec // a field offset
}

// bankStacks is what the bank holds now.
func bankStacks(mem *execMem) []selling.Row {
	bank, ok := selling.Bank(mem, liveLife)
	if !ok {
		return nil
	}
	return bank.Rows()
}

/*
The NPC side of the image: Main.npc, its slots, a shelf of templates, and the
two things that look like templates and are not.

Every slot is a real NPC object allocated behind one vtable, as the game
allocates them at world load -- that agreement is what tells the array from
anything else 201 long.

The addresses matter. A template is picked out of the heap by being inactive and
carrying the right netID, and the *last* match in address order is the one taken,
so the decoys are placed one either side of the shelf:

  - below it, a Blue Slime object that is inactive and is not a template -- a
    despawned NPC that left its netID behind, which is a thing the live game was
    observed doing. Its stats are the scaled ones. Taking the first match instead
    of the last hands this out.
  - above it, the live Blue Slime standing in the world, in slot zero, active and
    scaled. Dropping the inactive test hands *this* out, and it is above the shelf
    precisely so that dropping the test is not covered for by the ordering.

Both read as a Blue Slime and neither is one, which is the whole difficulty.
*/
const (
	npcVTable     = 0xFEEDFACE
	npcDecoyAt    = base + 0x7C000
	npcTemplateAt = base + 0x80000
	npcArrAt      = base + 0x88000
	npcObjectsAt  = base + 0x8A000
	npcStride     = 0x2A0 // NPC_OBJECT_SIZE rounded up, so two objects never touch
	npcArrayLen   = 201   // MAX_NPCS + 1, which is the length the finder checks

	// What a Blue Slime reads as once an expert world has scaled it, which is
	// what both decoys carry and no template ever does.
	npcScaledLife = 60
)

// npcTemplate is one entry on the template shelf.
type npcTemplate struct {
	netID   int32
	npcType int32
	life    int32
	damage  int32
	defense int32
	width   int32
	height  int32
}

/*
npcShelf is the templates the fixture offers.

A negative netID is in it because that is the case the whole keying exists for:
the coloured slimes share a type and differ only by the netID, so a scan keyed on
type would hand out the wrong one and read as correct.
*/
var npcShelf = []npcTemplate{
	{netID: 1, npcType: 1, life: 25, damage: 7, defense: 2, width: 24, height: 18},
	{netID: -3, npcType: 1, life: 45, damage: 9, defense: 4, width: 24, height: 18},
	{netID: 4, npcType: 4, life: 2800, damage: 15, defense: 12, width: 100, height: 110},
}

// npcTakenSlots are the slots already holding a live NPC, so the first free one
// is not slot zero -- which a search that never looked would also return.
var npcTakenSlots = []int{0, 1, 2}

// liveSlimeSlot is the slot holding the Blue Slime that is in the world, rather
// than the one on the shelf.
const liveSlimeSlot = 0

// plantNPCsInto writes Main.npc, its slots and the template shelf.
func plantNPCsInto(mem *execMem) {
	mem.PokeBytes(staticAt+uint32(layout.MainNPCOff), u32(npcArrAt)) //nolint:gosec // a field offset
	mem.PokeI32(npcArrAt+layout.ArrLenOff, npcArrayLen)
	for i := range npcArrayLen {
		obj := uint32(npcObjectsAt + i*npcStride) //nolint:gosec // a planted address
		mem.PokeBytes(npcArrAt+layout.ArrDataOff+uint32(i)*4, u32(obj))
		mem.PokeBytes(obj, u32(npcVTable))
		mem.PokeBytes(obj+layout.NPCActive, []byte{0})
		// A slot's netID is nothing the shelf offers, so a slot can never be
		// mistaken for the template of the NPC being asked for.
		mem.PokeI32(obj+layout.NPCNetID, 0)
	}
	for _, slot := range npcTakenSlots {
		obj := uint32(npcObjectsAt + slot*npcStride) //nolint:gosec // a planted address
		mem.PokeBytes(obj+layout.NPCActive, []byte{1})
	}
	// The Blue Slime in the world, and the one that used to be.
	slime := uint32(npcObjectsAt + liveSlimeSlot*npcStride) //nolint:gosec // a planted address
	mem.PokeI32(slime+layout.NPCNetID, 1)
	mem.PokeI32(slime+layout.NPCLifeMax, npcScaledLife)
	mem.PokeBytes(npcDecoyAt, u32(npcVTable))
	mem.PokeI32(npcDecoyAt+layout.NPCNetID, 1)
	mem.PokeI32(npcDecoyAt+layout.NPCLifeMax, npcScaledLife)
	mem.PokeBytes(npcDecoyAt+layout.NPCActive, []byte{0})
	for i, tpl := range npcShelf {
		obj := uint32(npcTemplateAt + i*npcStride) //nolint:gosec // a planted address
		mem.PokeBytes(obj, u32(npcVTable))
		mem.PokeI32(obj+layout.NPCNetID, tpl.netID)
		mem.PokeI32(obj+layout.NPCType, tpl.npcType)
		mem.PokeI32(obj+layout.NPCLifeMax, tpl.life)
		mem.PokeI32(obj+layout.NPCDamage, tpl.damage)
		mem.PokeI32(obj+layout.NPCDefense, tpl.defense)
		mem.PokeI32(obj+layout.NPCWidth, tpl.width)
		mem.PokeI32(obj+layout.NPCHeight, tpl.height)
		mem.PokeBytes(obj+layout.NPCActive, []byte{0})
	}
}

// pyPlantNPCs is the same shelf and the same slots, as Python source.
func pyPlantNPCs() string {
	var b strings.Builder
	fmt.Fprintf(&b, `
from terrariabonker import npcs as N
mem.poke_bytes(%d + N.MAIN_NPC_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + I.ARR_LEN_OFF, %d)
for _i in range(%d):
    _obj = %d + _i * %d
    mem.poke_bytes(%d + I.ARR_DATA_OFF + _i * 4, struct.pack("<I", _obj))
    mem.poke_bytes(_obj, struct.pack("<I", %d))
    u8(_obj + N.NPC_ACTIVE, 0)
    mem.poke_i32(_obj + N.NPC_NET_ID, 0)
for _slot in %s:
    u8(%d + _slot * %d + N.NPC_ACTIVE, 1)
mem.poke_i32(%d + N.NPC_NET_ID, 1)
mem.poke_i32(%d + N.NPC_LIFE_MAX, %d)
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + N.NPC_NET_ID, 1)
mem.poke_i32(%d + N.NPC_LIFE_MAX, %d)
u8(%d + N.NPC_ACTIVE, 0)
`, staticAt, npcArrAt, npcArrAt, npcArrayLen, npcArrayLen, npcObjectsAt, npcStride,
		npcArrAt, npcVTable, pyInts(npcTakenSlots), npcObjectsAt, npcStride,
		npcObjectsAt+liveSlimeSlot*npcStride,
		npcObjectsAt+liveSlimeSlot*npcStride, npcScaledLife,
		npcDecoyAt, npcVTable, npcDecoyAt, npcDecoyAt, npcScaledLife, npcDecoyAt)
	for i, tpl := range npcShelf {
		obj := npcTemplateAt + i*npcStride
		fmt.Fprintf(&b, `
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + N.NPC_NET_ID, %d)
mem.poke_i32(%d + N.NPC_TYPE, %d)
mem.poke_i32(%d + N.NPC_LIFE_MAX, %d)
mem.poke_i32(%d + N.NPC_DAMAGE, %d)
mem.poke_i32(%d + N.NPC_DEFENSE, %d)
mem.poke_i32(%d + N.NPC_WIDTH, %d)
mem.poke_i32(%d + N.NPC_HEIGHT, %d)
u8(%d + N.NPC_ACTIVE, 0)
`, obj, npcVTable, obj, tpl.netID, obj, tpl.npcType, obj, tpl.life, obj, tpl.damage,
			obj, tpl.defense, obj, tpl.width, obj, tpl.height, obj)
	}
	return b.String()
}

// pyInts is a slot list as a Python list.
func pyInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// plantFacingInto is which way the player is turned, which is the side a spawn
// lands on.
func plantFacingInto(mem *execMem, facing int32) {
	mem.PokeI32(uint32(liveLife)-0x738+0x2C, facing)
}

// plantFacing is the same, as Python source.
func plantFacing(facing int32) string {
	return fmt.Sprintf("mem.poke_i32(%d + 0x2C, %d)\n", liveLife-0x738, facing)
}
