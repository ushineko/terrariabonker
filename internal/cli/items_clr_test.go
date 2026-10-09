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
	mem := &execMem{memtest.New(clrBase, 0x20000)}
	mem.PlantCLRString(clrBase+0x40, "terrariabonker")
	mem.PlantCLRPlayer(clrBase+0x800, []int32{400, 400, 390, 200, 200, 200}, clrBase+0x40)
	mem.PlantCLRInventory(clrBase+0x800, clrBase+0xC000, clrItems, []memtest.CLRItem{
		{Slot: 0, Type: 757, Stack: 1, Damage: 85},
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
A new type or a modifier is refused under .NET Framework, with nothing written:
both copy from the pristine template at the template span's offsets, which only
the mono entry has.
*/
func TestUnderTheCLRTypeAndModifierChangesAreRefused(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	for _, argv := range [][]string{
		{"set-item", "0", "2"},
		{"set-item", "0", "757", "--prefix", "81"},
		{"give", "2"},
	} {
		mem := clrArmoury()
		before := mem.Hex()
		code, _, errOut := runUnder(t, runtime, mem, argv...)
		require.NotZero(t, code, "%v ran under the CLR entry", argv)
		require.Contains(t, errOut, runtime, "%v", argv)
		require.Equal(t, before, mem.Hex(), "%v changed something", argv)
	}
}
