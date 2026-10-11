package selling_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/selling"
)

/*
A container under .NET Framework (spec 052).

Planted with literals from the measurement (cmd/clrfields against 1.4.5.8 on .NET
Framework 4.8.1, docs/clr-fields-1.4.5.8.txt), not with internal/layout's
numbers: Player.bank at 0x0E0 against statLife 0x470, Chest.item at 0x004, an
szarray's length at +4 and elements from +8, and in an Item type 0x050, stack
0x064, value 0x0D8 and favorited 0x10C -- nowhere near the others, where under
mono it sits between type and stack.
*/

const (
	clrPlayer = base + 0x100
	clrLife   = clrPlayer + 0x470
)

func netfx(t *testing.T) layout.Entry {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return e
}

// plantCLR builds a player with a Piggy Bank holding bankItems, the CLR way.
func plantCLR(slots int32) *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(clrPlayer+0x0E0, u32(chest))
	mem.PokeBytes(chest+0x04, u32(arr))
	mem.PokeI32(arr+0x04, slots)
	for i, it := range bankItems {
		at := uint32(items + i*0x200) //nolint:gosec // a planted address
		mem.PokeBytes(arr+0x08+uint32(i)*4, u32(at))
		mem.PokeI32(at+0x050, it.itemType)
		mem.PokeI32(at+0x064, it.stack)
		mem.PokeI32(at+0x0D8, it.value)
		if it.favorited {
			mem.PokeBytes(at+0x10C, []byte{1})
		}
	}
	return mem
}

// Every slot of the CLR bank reads back as what was planted at the CLR offsets.
func TestReadingTheCLRBank(t *testing.T) {
	bank, ok := selling.Bank(plantCLR(layout.BankSlots), netfx(t), clrLife)
	require.True(t, ok, "the bank did not read as one")

	got := bank.Rows()
	require.Len(t, got, len(bankItems))
	for i, want := range bankItems {
		require.Equalf(t, selling.Row{
			Index: i, Addr: uint32(items + i*0x200), //nolint:gosec // a planted address
			Type: want.itemType, Stack: want.stack, Favorited: want.favorited, Value: want.value,
		}, got[i], "slot %d", i)
	}
}

// The mono numbers find no bank in a CLR player, and the CLR numbers none in a
// mono one: a container is read with its own entry's offsets or not at all.
func TestABankIsReadWithItsOwnEntrysNumbers(t *testing.T) {
	_, ok := selling.Bank(plantCLR(layout.BankSlots), layout.Mono(), clrLife)
	require.False(t, ok, "mono's offsets found a bank in a CLR player")
	_, ok = selling.Bank(plant(layout.BankSlots), netfx(t), life)
	require.False(t, ok, "the CLR's offsets found a bank in a mono player")
}

// A CLR chest whose length is not the bank's is not the bank.
func TestACLRChestOfTheWrongLengthIsNotTheBank(t *testing.T) {
	for _, slots := range []int32{0, 39, 41, layout.BankSlots} {
		_, ok := selling.Bank(plantCLR(slots), netfx(t), clrLife)
		require.Equalf(t, slots == layout.BankSlots, ok, "a chest of %d slots was judged wrongly", slots)
	}
}

/*
A CLR write checks the slot first, then lands where the CLR keeps the field: a
stack at 0x064, a bare type at 0x050, and a template block from 0x018 -- past the
MethodTable and the five references, which a copy must never carry from one
object to another.
*/
func TestACLRWriteChecksTheSlotFirst(t *testing.T) {
	mem := plantCLR(layout.BankSlots)
	bank, _ := selling.Bank(mem, netfx(t), clrLife)
	before := mem.Hex()
	seven := int32(7)

	require.False(t, bank.Write(0, selling.Change{ExpectType: 757, ExpectStack: 99, Stack: &seven}),
		"a slot holding a different item was written")
	require.False(t, bank.Write(0, selling.Change{ExpectType: 9, ExpectStack: 1, Stack: &seven}),
		"a slot holding a different count was written")
	require.Equal(t, before, mem.Hex(), "a refused write wrote anyway")

	require.True(t, bank.Write(0, selling.Change{ExpectType: 9, ExpectStack: 99, Stack: &seven}))
	stack, _ := mem.ReadI32(items + 0x064)
	require.EqualValues(t, 7, stack)

	coin := int32(72)
	require.True(t, bank.Write(1, selling.Change{ExpectType: 71, ExpectStack: 50, ItemType: &coin}))
	itemType, _ := mem.ReadI32(items + 0x200 + 0x050)
	require.EqualValues(t, 72, itemType)

	block := make([]byte, 0x40)
	for i := range block {
		block[i] = 0xEE
	}
	mem.PokeBytes(items+0x400, []byte{0xAA, 0xAA, 0xAA, 0xAA}) // the MethodTable
	mem.PokeBytes(items+0x400+0x14, []byte{0xBB, 0xBB, 0xBB, 0xBB})
	require.True(t, bank.Write(2, selling.Change{ExpectType: 757, ExpectStack: 1, Block: block}))
	require.Equal(t, []byte{0xAA, 0xAA, 0xAA, 0xAA}, mem.Read(items+0x400, 4), "the block ran over the header")
	require.Equal(t, []byte{0xBB, 0xBB, 0xBB, 0xBB}, mem.Read(items+0x400+0x14, 4),
		"the block ran over the last reference")
	require.Equal(t, block, mem.Read(items+0x400+0x18, len(block)))
}
