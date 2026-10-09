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
	e, support := layout.Select("1.4.5.8+24893155", "netfx-4.8.9345.0")
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
With no measurement of the held slot, the inventory says it cannot tell.

The CLR entry has none (no selectedItem field; spec 052). Reading offset 0 would
read statLife itself, and a player on 9 life or less would be "holding" a slot.
*/
func TestTheHeldSlotIsUnknownWithoutAMeasurement(t *testing.T) {
	mem, inv := clrInventory(t, nil)
	mem.PokeI32(cLife, 7) // a statLife that looks like a hotbar index
	_, ok := inv.SelectedSlot()
	require.False(t, ok)
}
