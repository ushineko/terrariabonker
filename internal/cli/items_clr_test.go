package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
)

const (
	clrBase  = 0x10000000
	clrItems = clrBase + 0xD000
)

// clrArmoury is a CLR game holding a sword in slot 0 and a pickaxe in slot 3.
func clrArmoury() *execMem {
	mem := &execMem{FakeMem: memtest.New(clrBase, 0x80000)}
	mem.PlantCLRString(clrBase+0x40, "terrariabonker")
	mem.PlantCLRPlayer(clrBase+0x800, []int32{400, 400, 390, 200, 200, 200}, clrBase+0x40)
	mem.PlantCLRInventory(clrBase+0x800, clrBase+0xC000, clrItems, []memtest.CLRItem{
		{Slot: 0, Type: 757, Stack: 1, Damage: 85},
		// An empty slot is an Item object of type 0, as in the game, not a null.
		{Slot: 1},
		{Slot: 3, Type: 1294, Stack: 1, Damage: 39, Pick: 210, UseTime: 15, UseAnim: 15},
	})
	return mem
}

// field is one int of the item in a slot, at a CLR item offset.
func field(mem *execMem, slot int, off uint32) int32 {
	v, _ := mem.ReadI32(clrItems + uint32(slot)*memtest.CLRItemSize + off) //nolint:gosec // a slot index
	return v
}

/*
Under .NET Framework an item's fields are edited in place, at the CLR's offsets.

set-stack, set-item with the same type, fast-mining and long-reach each change
the fields they name and nothing else of the item.
*/
func TestUnderTheCLRItemFieldsAreEdited(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrArmoury()

	code, _, errOut := runUnder(t, runtime, mem, "set-stack", "3", "5")
	require.Zero(t, code, errOut)
	require.EqualValues(t, 5, field(mem, 3, memtest.CLRItemStack))

	code, _, errOut = runUnder(t, runtime, mem, "set-item", "0", "757", "--damage", "200")
	require.Zero(t, code, errOut)
	require.EqualValues(t, 200, field(mem, 0, memtest.CLRItemDamage))
	require.EqualValues(t, 757, field(mem, 0, memtest.CLRItemType), "the type moved")

	code, _, errOut = runUnder(t, runtime, mem, "fast-mining")
	require.Zero(t, code, errOut)
	require.EqualValues(t, 8, field(mem, 3, memtest.CLRItemUseTime))
	require.EqualValues(t, 13, field(mem, 3, memtest.CLRItemUseAnim))
	require.EqualValues(t, 200, field(mem, 3, memtest.CLRItemPick))
	require.EqualValues(t, 0, field(mem, 0, memtest.CLRItemUseTime), "the sword was treated as a pickaxe")

	code, _, errOut = runUnder(t, runtime, mem, "long-reach")
	require.Zero(t, code, errOut)
	require.EqualValues(t, 20, field(mem, 0, memtest.CLRItemTileBoost))
	require.EqualValues(t, 20, field(mem, 3, memtest.CLRItemTileBoost))
}

/*
Under .NET Framework a new type, a gift and a modifier copy from the game's
pristine template, at the CLR's copy span.

Two templates are planted below the inventory, so the scan meets a template
before the player's own item of the same type, as with the game's master copies.
Type 2's template carries a damage of -1 where the sword had 85, so a copied
block is visible; Legendary (81) multiplies damage by 1.15 from the template's base
85, which the game rounds to 98.
*/
func TestUnderTheCLRTemplatesAreCopied(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	plant := func() *execMem {
		mem := clrArmoury()
		mem.PlantCLRTemplate(clrBase+0x2000, 2, map[uint32]int32{memtest.CLRItemStack: 1, memtest.CLRItemDamage: -1})
		mem.PlantCLRTemplate(clrBase+0x2400, 757, map[uint32]int32{memtest.CLRItemStack: 1, memtest.CLRItemDamage: 85})
		return mem
	}

	mem := plant()
	code, _, errOut := runUnder(t, runtime, mem, "set-item", "0", "2")
	require.Zero(t, code, errOut)
	require.EqualValues(t, 2, field(mem, 0, memtest.CLRItemType))
	require.EqualValues(t, -1, field(mem, 0, memtest.CLRItemDamage), "the template's fields were not copied")

	mem = plant()
	code, out, errOut := runUnder(t, runtime, mem, "give", "2", "--stack", "7")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "slot 1")
	require.EqualValues(t, 2, field(mem, 1, memtest.CLRItemType))
	require.EqualValues(t, 7, field(mem, 1, memtest.CLRItemStack))

	mem = plant()
	code, _, errOut = runUnder(t, runtime, mem, "set-item", "0", "757", "--prefix", "81")
	require.Zero(t, code, errOut)
	b := mem.Read(clrItems+memtest.CLRItemPrefix, 1)
	require.Equal(t, []byte{81}, b, "the modifier byte")
	require.EqualValues(t, 98, field(mem, 0, memtest.CLRItemDamage), "damage scaled from the template's base")
}
