package selling_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/selling"
)

/*
Selling is arithmetic that takes the player's items away.

Getting the price wrong in the player's favour is a nuisance; getting it wrong
the other way, or writing to a slot that has moved, takes something they cannot
get back. So the sums are compared across their whole range and the write is
compared for what it refuses as much as for what it does.
*/

/*
A price is the same price, including the floor that stops a big stack of nearly
worthless items being worth nothing.

The value is five times the copper worth, so the cases walk either side of each
multiple of five as well as the ordinary numbers.
*/
func TestPrice(t *testing.T) {
	// A stack of each of these, for every value: the numbers either side of a
	// multiple of five, because the value is five times the copper worth.
	stacks := []int32{-1, 0, 1, 2, 99, 1000}

	for _, c := range []struct {
		value int32
		want  []int32
	}{
		{value: -5, want: []int32{0, 0, 0, 0, 0, 0}},
		{value: 0, want: []int32{0, 0, 0, 0, 0, 0}},
		{value: 1, want: []int32{0, 0, 1, 2, 99, 1000}},
		{value: 4, want: []int32{0, 0, 1, 2, 99, 1000}},
		{value: 5, want: []int32{0, 0, 1, 2, 99, 1000}},
		{value: 6, want: []int32{0, 0, 1, 2, 99, 1000}},
		{value: 9, want: []int32{0, 0, 1, 2, 99, 1000}},
		{value: 10, want: []int32{0, 0, 2, 4, 198, 2000}},
		{value: 499, want: []int32{0, 0, 99, 198, 9801, 99000}},
		{value: 500, want: []int32{0, 0, 100, 200, 9900, 100000}},
		{value: 5000000, want: []int32{0, 0, 1000000, 2000000, 99000000, 1000000000}},
	} {
		for i, stack := range stacks {
			require.Equalf(t, c.want[i], selling.Price(c.value, stack),
				"a stack of %d worth %d", stack, c.value)
		}
	}
}

/*
Change is broken into coins, largest first.

The order is what a caller writes into the slots, and the floor below each
denomination is what stops a hundred silver sitting where one gold belongs --
which the game would never leave and a player would notice.
*/
func TestCoinStacks(t *testing.T) {
	for _, c := range []struct {
		copper int32
		want   [][2]int32
	}{
		{copper: -1},
		{copper: 0},
		{copper: 1, want: [][2]int32{{71, 1}}},
		{copper: 99, want: [][2]int32{{71, 99}}},
		{copper: 100, want: [][2]int32{{72, 1}}},
		{copper: 101, want: [][2]int32{{72, 1}, {71, 1}}},
		{copper: 9999, want: [][2]int32{{72, 99}, {71, 99}}},
		{copper: 10000, want: [][2]int32{{73, 1}}},
		{copper: 999999, want: [][2]int32{{73, 99}, {72, 99}, {71, 99}}},
		{copper: 1000000, want: [][2]int32{{74, 1}}},
		{copper: 1010101, want: [][2]int32{{74, 1}, {73, 1}, {72, 1}, {71, 1}}},
		{copper: 2147483647, want: [][2]int32{{74, 2147}, {73, 48}, {72, 36}, {71, 47}}},
	} {
		got := selling.CoinStacks(c.copper)
		require.Lenf(t, got, len(c.want), "%d copper is broken into a different number of stacks", c.copper)
		for j, w := range c.want {
			require.Equalf(t, w[0], got[j].Type, "%d copper: coin %d is a different one", c.copper, j)
			require.Equalf(t, w[1], got[j].Stack, "%d copper: coin %d is a different count", c.copper, j)
		}
		// Nothing below a hundred of a denomination the next one up could carry.
		for _, coin := range got {
			if coin.Type != selling.CoinTypes[len(selling.CoinTypes)-1] {
				require.Lessf(t, coin.Stack, int32(selling.CoinMaxStack),
					"%d copper left a stack the next denomination should have carried", c.copper)
			}
		}
	}
}

// Where the planted bank and its items go.
const (
	base  = 0x10000000
	size  = 0x10000
	life  = base + 0x800
	chest = base + 0x1000
	arr   = base + 0x1100
	items = base + 0x2000
)

// plant builds a player with a Piggy Bank holding a few things.
func plant(slots int32) *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(uint32(int64(life)+layout.BankPtrOff), u32(chest)) //nolint:gosec // a planted address
	mem.PokeBytes(chest+layout.ChestItemOff, u32(arr))
	mem.PokeI32(arr+layout.ArrLenOff, slots)

	for i, it := range bankItems {
		at := uint32(items + i*0x200) //nolint:gosec // a planted address
		mem.PokeBytes(arr+layout.ArrDataOff+uint32(i)*4, u32(at))
		mem.PokeI32(at+uint32(layout.ItemType), it.itemType)
		mem.PokeI32(at+uint32(layout.ItemStack), it.stack)
		mem.PokeI32(at+layout.ItemValue, it.value)
		if it.favorited {
			mem.PokeBytes(at+uint32(layout.ItemFavorited), []byte{1})
		}
	}
	return mem
}

// bankItems is what the planted bank holds.
var bankItems = []struct {
	itemType, stack, value int32
	favorited              bool
}{
	{itemType: 9, stack: 99, value: 5},       // wood
	{itemType: 71, stack: 50, value: 5},      // copper coins
	{itemType: 757, stack: 1, value: 100000}, // a staff
	{itemType: 757, stack: 1, value: 189062}, // the same staff, with a modifier
	{itemType: 3509, stack: 1, value: 250, favorited: true},
}

/*
Every slot of the bank reads back as what was planted, including the modifier
that moved a price.

The two copies of one staff are planted with different values, which is the
whole reason the price comes off the live item rather than off its type's
template: a modifier changes what a thing is worth.
*/
func TestReadingTheBank(t *testing.T) {
	bank, ok := selling.Bank(plant(layout.BankSlots), life)
	require.True(t, ok, "the bank did not read as one")

	got := bank.Rows()
	require.Len(t, got, len(bankItems), "a different number of slots holds something")
	for i, want := range bankItems {
		require.Equalf(t, i, got[i].Index, "slot %d is a different index", i)
		require.Equalf(t, uint32(items+i*0x200), got[i].Addr, "slot %d is at a different address", i)
		require.Equalf(t, want.itemType, got[i].Type, "slot %d holds a different item", i)
		require.Equalf(t, want.stack, got[i].Stack, "slot %d holds a different count", i)
		require.Equalf(t, want.value, got[i].Value, "slot %d is worth something else", i)
		require.Equalf(t, want.favorited, got[i].Favorited, "slot %d is favorited differently", i)
	}
	require.NotEqual(t, got[2].Value, got[3].Value,
		"the two copies of one item read the same value, so the fixture proves nothing")
}

/*
A chest whose length is not the bank's is not the bank.

The length is the cheapest signal that a pointer is really a chest, and a wrong
offset is far more likely to miss it than to hit it.
*/
func TestAChestOfTheWrongLengthIsNotTheBank(t *testing.T) {
	for _, slots := range []int32{0, 39, 41, layout.BankSlots} {
		_, ok := selling.Bank(plant(slots), life)
		require.Equalf(t, slots == layout.BankSlots, ok,
			"a chest of %d slots was judged wrongly", slots)
	}
}

/*
A write goes ahead only when the slot still holds what the caller believed.

The caller reads forty slots and then writes to them, which is a wide window, and
opening a chest is exactly the kind of allocation that moves objects. A slot that
changed underneath is a reason to stop rather than a value to overwrite.
*/
func TestAWriteChecksTheSlotFirst(t *testing.T) {
	for _, c := range []struct {
		name        string
		expectType  int32
		expectStack int32
		ok          bool
	}{
		{"what is really there", 9, 99, true},
		{"a different item", 757, 99, false},
		{"a different count", 9, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			zero := int32(0)
			mem := plant(layout.BankSlots)
			before := mem.Hex()

			bank, _ := selling.Bank(mem, life)
			got := bank.Write(0, selling.Change{
				ExpectType: c.expectType, ExpectStack: c.expectStack, Stack: &zero,
			})
			require.Equal(t, c.ok, got, "the write was judged differently")
			if c.ok {
				require.NotEqual(t, before, mem.Hex(), "an allowed write wrote nothing")
			} else {
				require.Equal(t, before, mem.Hex(), "a refused write wrote anyway")
			}
		})
	}
}

// A slot outside the container, or one with no item, is refused rather than
// addressed.
func TestASlotThatIsNotThere(t *testing.T) {
	mem := plant(layout.BankSlots)
	bank, _ := selling.Bank(mem, life)
	before := mem.Hex()

	for _, index := range []int{-1, len(bankItems), layout.BankSlots, layout.BankSlots + 1} {
		_, ok := bank.ItemAddr(index)
		require.Falsef(t, ok, "slot %d resolved to an address", index)
		_, ok = bank.Read(index)
		require.Falsef(t, ok, "slot %d was read", index)

		zero := int32(0)
		require.Falsef(t, bank.Write(index, selling.Change{Stack: &zero}),
			"slot %d was written to", index)
	}
	require.Equal(t, before, mem.Hex(), "something was written anyway")
}
