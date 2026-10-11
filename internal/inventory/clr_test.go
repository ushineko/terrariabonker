package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/inventory"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

const cBase, cLife = 0x10000000, 0x10000800

func clrEntry(t *testing.T) layout.Entry {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return e
}

func clrInventory(t *testing.T, items []memtest.CLRItem) (*memtest.FakeMem, *inventory.Inventory) {
	t.Helper()
	mem := memtest.New(cBase, 0x20000)
	mem.PlantCLRInventory(cLife, cBase+0xC000, cBase+0xD000, items)
	return mem, inventory.NewFor(clrEntry(t), mem, cLife)
}

// The CLR inventory reads the planted items' fields, at the CLR offsets.
func TestTheCLRInventoryReadsItsItems(t *testing.T) {
	_, inv := clrInventory(t, []memtest.CLRItem{
		{Slot: 0, Type: 757, Stack: 1, Damage: 85, Prefix: 81, AutoReuse: true},
		{Slot: 9, Type: 2, Stack: 250},
	})
	s, ok := inv.ReadSlot(0)
	require.True(t, ok)
	require.Equal(t, inventory.Slot{Index: 0, ItemAddr: s.ItemAddr, Type: 757, Stack: 1, Damage: 85,
		Prefix: 81, AutoReuse: 1}, s)
	require.Equal(t, []int{9}, inv.FindType(2))
	require.Equal(t, 2, inv.NonemptyCount())
}

/*
The potion check reads one span per item, and under the CLR the four fields it
needs are in a different order: stack and buffType below favorited, where under
mono they are above. A span assumed from mono's order would miss them.
*/
func TestTheCLRPotionCheckFindsItsFields(t *testing.T) {
	_, inv := clrInventory(t, []memtest.CLRItem{
		{Slot: 4, Type: 2347, Stack: 5, Favorited: true, Consumable: true, BuffType: 104},
		{Slot: 5, Type: 2348, Stack: 5, Favorited: false, Consumable: true, BuffType: 105},
		{Slot: 6, Type: 2349, Stack: 1, Favorited: true, Consumable: true, BuffType: 106},
	})
	require.Equal(t, []inventory.Potion{{Slot: 4, Buff: 104}}, inv.FavoritedPotions(2))
}

/*
The CLR held slot is the `selected` word of the inlined SelectedItemState struct,
read as an offset from statLife (spec 052): an in-range slot reads back, and a
fishing rod in that slot reads as holding a rod -- the guard auto-catch recast
needs before it presses the use button at empty water.
*/
func TestTheCLRHeldSlotReadsSelectedItemState(t *testing.T) {
	e := clrEntry(t)
	mem, inv := clrInventory(t, []memtest.CLRItem{{Slot: 3, Type: 2294, Stack: 1}})
	rod := uint32(cBase + 0xD000 + 3*memtest.CLRItemSize) // slot 3's item object
	mem.PokeI32(rod+uint32(e.Item.FishingPole), 50)
	mem.PokeI32(cLife+uint32(e.Player.SelectedItemFromLife), 3) //nolint:gosec // a positive offset

	slot, ok := inv.SelectedSlot()
	require.True(t, ok)
	require.Equal(t, 3, slot)
	require.True(t, inv.HoldingRod())
}

/*
A selected word outside the hotbar (0..9) says the held slot cannot be told, so a
stray value never names a slot and auto-catch does not press against it.
*/
func TestTheCLRHeldSlotRejectsOutOfRange(t *testing.T) {
	e := clrEntry(t)
	mem, inv := clrInventory(t, nil)
	mem.PokeI32(cLife+uint32(e.Player.SelectedItemFromLife), 42) //nolint:gosec // a positive offset
	_, ok := inv.SelectedSlot()
	require.False(t, ok)
	require.False(t, inv.HoldingRod())
}
