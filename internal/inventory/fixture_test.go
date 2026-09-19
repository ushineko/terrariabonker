package inventory_test

import (
	"fmt"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
One inventory, planted into a fake.

Each field is named rather than written at a number: the offset is looked up in
the one table that declares it, so a fixture cannot quietly agree with a reader
that has an offset wrong -- both would have to be wrong the same way, and the
table is compared against itself elsewhere.
*/

const (
	base = 0x10000000
	size = 0x20000
	life = base + 0x1000
	arr  = base + 0x2000
	// items are spaced far enough apart that a field written past the end of
	// one is visible rather than silently landing in the next.
	itemBase   = base + 0x4000
	itemStride = 0x400
)

// field is one value planted into an item, named as the Python names it.
type field struct {
	name  string
	kind  string // "i32", "u8" or "f32"
	value float64
}

// item is one slot of the planted inventory. A slot with no fields at all has
// no object in it, which is a different thing from an empty slot: the array
// entry is null rather than pointing at an Item whose type is zero.
type item struct {
	slot   int
	absent bool
	fields []field
}

/*
inventoryImage is the planted inventory.

It covers what each reader has to get right: a pickaxe for the mining sweep, a
favorited potion and three near misses for the potion gate, a rod and a bait for
the fishing sweep, an item with float fields for the modifier arithmetic, a slot
that is empty, and a slot with no object at all.
*/
var inventoryImage = []item{
	{slot: 0, fields: []field{ // a pickaxe, held: the hotbar slot below points here
		{"ITEM_TYPE", "i32", 3509}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 100}, {"ITEM_USE_TIME", "i32", 20},
		{"ITEM_USE_ANIM", "i32", 25}, {"ITEM_DAMAGE", "i32", 8},
		{"ITEM_RARE", "i32", 1}, {"ITEM_AUTOREUSE", "u8", 1},
		{"ITEM_MELEE", "u8", 1}, {"ITEM_TILEBOOST", "i32", 3},
	}},
	{slot: 1, fields: []field{ // a favorited healing potion: the one that counts
		{"ITEM_TYPE", "i32", 188}, {"ITEM_STACK", "i32", 5},
		{"ITEM_FAVORITED", "u8", 1}, {"ITEM_CONSUMABLE", "u8", 1},
		{"ITEM_BUFF_TYPE", "i32", 11},
	}},
	{slot: 2, fields: []field{ // the same potion, not favorited
		{"ITEM_TYPE", "i32", 188}, {"ITEM_STACK", "i32", 5},
		{"ITEM_CONSUMABLE", "u8", 1}, {"ITEM_BUFF_TYPE", "i32", 11},
	}},
	{slot: 3, fields: []field{ // a favorited pet: a buff, and not consumable
		{"ITEM_TYPE", "i32", 1927}, {"ITEM_STACK", "i32", 1},
		{"ITEM_FAVORITED", "u8", 1}, {"ITEM_BUFF_TYPE", "i32", 40},
	}},
	{slot: 4, fields: []field{ // favorited, consumable, and a stack of one
		{"ITEM_TYPE", "i32", 189}, {"ITEM_STACK", "i32", 1},
		{"ITEM_FAVORITED", "u8", 1}, {"ITEM_CONSUMABLE", "u8", 1},
		{"ITEM_BUFF_TYPE", "i32", 12},
	}},
	{slot: 5, fields: []field{ // a fishing rod
		{"ITEM_TYPE", "i32", 2294}, {"ITEM_STACK", "i32", 1},
		{"ITEM_FISHING_POLE", "u8", 25},
	}},
	{slot: 6, fields: []field{ // bait
		{"ITEM_TYPE", "i32", 2675}, {"ITEM_STACK", "i32", 12},
		{"ITEM_BAIT", "u8", 15},
	}},
	{slot: 7, fields: []field{ // a second rod: both are reported, not the first
		{"ITEM_TYPE", "i32", 2289}, {"ITEM_STACK", "i32", 1},
		{"ITEM_FISHING_POLE", "u8", 40},
	}},
	{slot: 8, fields: []field{ // a rod's power on an item of type zero: not carried
		{"ITEM_FISHING_POLE", "u8", 99},
	}},
	{slot: 9, fields: []field{ // a sword, and the float fields a modifier scales
		{"ITEM_TYPE", "i32", 4}, {"ITEM_STACK", "i32", 1},
		{"ITEM_DAMAGE", "i32", 12}, {"ITEM_KNOCKBACK", "f32", 5.5},
		{"ITEM_SCALE", "f32", 1}, {"ITEM_SHOOTSPEED", "f32", 0},
		{"ITEM_MANA", "i32", 0}, {"ITEM_CRIT", "i32", 0},
		{"ITEM_PREFIX", "u8", 0}, {"ITEM_ACCESSORY", "u8", 0},
		{"ITEM_MELEE", "u8", 1}, {"ITEM_DEFENSE", "i32", 0},
	}},
	{slot: 11, fields: []field{ // a pickaxe outside the hotbar, for the sweep
		{"ITEM_TYPE", "i32", 3503}, {"ITEM_STACK", "i32", 1},
		{"ITEM_PICK", "i32", 35}, {"ITEM_USE_TIME", "i32", 23},
		{"ITEM_USE_ANIM", "i32", 23},
	}},
	{slot: 12, absent: true}, // no object at all
	{slot: 13},               // an object whose type is zero: an empty slot
}

// selectedSlot is the hotbar slot the planted player is holding, which is the
// pickaxe and not the rod: holding_rod has to say no here.
const selectedSlot = 0

// itemAddr is where the object for a slot goes.
func itemAddr(slot int) uint32 {
	return uint32(itemBase + slot*itemStride) //nolint:gosec // a planted address
}

// plant writes the image into a Go fake and returns it.
func plant() *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(uint32(life+layout.InventoryPtrOff), u32(arr)) //nolint:gosec // a planted address
	mem.PokeI32(uint32(life+layout.SelectedItemOff), selectedSlot)
	for _, it := range inventoryImage {
		if it.absent {
			continue
		}
		addr := itemAddr(it.slot)
		mem.PokeBytes(uint32(arr+layout.ArrDataOff)+uint32(it.slot)*4, u32(addr)) //nolint:gosec // a planted address
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
	return mem
}

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// layoutInventoryPtrOff and itoa keep the tests above readable. The offset comes
// from the one table that declares it.
var layoutInventoryPtrOff = int(layout.Offsets["INVENTORY_PTR_OFF"])

func itoa(v int) string { return fmt.Sprintf("%d", v) }

// layoutSelectedItemOff is the held-slot field, from the same table.
var layoutSelectedItemOff = int(layout.Offsets["SELECTED_ITEM_OFF"])
