package selling_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

/*
A price is the same price, including the floor that stops a big stack of nearly
worthless items being worth nothing.

The value is five times the copper worth, so the cases walk either side of each
multiple of five as well as the ordinary numbers.
*/
func TestPriceMatchesThePython(t *testing.T) {
	var cases [][2]int32
	for _, value := range []int32{-5, 0, 1, 4, 5, 6, 9, 10, 499, 500, 5000000} {
		for _, stack := range []int32{-1, 0, 1, 2, 99, 1000} {
			cases = append(cases, [2]int32{value, stack})
		}
	}
	var want []int32
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import selling
print(json.dumps([selling.sell_price(v, s) for v, s in %s]))`, pyPairs(cases)), &want)

	for i, c := range cases {
		require.Equalf(t, want[i], selling.Price(c[0], c[1]),
			"a stack of %d worth %d is priced differently", c[1], c[0])
	}
}

// Change is broken into the same coins, in the same order.
func TestCoinStacksMatchThePython(t *testing.T) {
	amounts := []int32{-1, 0, 1, 99, 100, 101, 9999, 10000, 999999, 1000000,
		1010101, 2147483647}

	var want [][][2]int32
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import selling
print(json.dumps([selling.coin_stacks(c) for c in %s]))`, pyInts(amounts)), &want)

	for i, copper := range amounts {
		got := selling.CoinStacks(copper)
		require.Lenf(t, got, len(want[i]), "%d copper is broken into a different number of stacks", copper)
		for j, w := range want[i] {
			require.Equalf(t, w[0], got[j].Type, "%d copper: coin %d is a different one", copper, j)
			require.Equalf(t, w[1], got[j].Stack, "%d copper: coin %d is a different count", copper, j)
		}
		// Nothing below a hundred of a denomination the next one up could carry.
		for _, c := range got {
			if c.Type != selling.CoinTypes[len(selling.CoinTypes)-1] {
				require.Lessf(t, c.Stack, int32(selling.CoinMaxStack),
					"%d copper left a stack the next denomination should have carried", copper)
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

// pyPlant is the same bank as Python source.
func pyPlant(slots int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import inventory as I, selling
mem = FakeMem(%d, %d)
mem.poke_bytes(%d + selling.BANK_PTR_OFF, struct.pack("<I", %d))
mem.poke_bytes(%d + selling.CHEST_ITEM_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + I.ARR_LEN_OFF, %d)
`, base, size, life, chest, chest, arr, arr, slots)
	for i, it := range bankItems {
		at := items + i*0x200
		fmt.Fprintf(&b, "mem.poke_bytes(%d + I.ARR_DATA_OFF + %d * 4, struct.pack(\"<I\", %d))\n",
			arr, i, at)
		fmt.Fprintf(&b, "mem.poke_i32(%d + I.ITEM_TYPE, %d)\n", at, it.itemType)
		fmt.Fprintf(&b, "mem.poke_i32(%d + I.ITEM_STACK, %d)\n", at, it.stack)
		fmt.Fprintf(&b, "mem.poke_i32(%d + selling.ITEM_VALUE, %d)\n", at, it.value)
		if it.favorited {
			fmt.Fprintf(&b, "mem.poke_bytes(%d + I.ITEM_FAVORITED, bytes([1]))\n", at)
		}
	}
	fmt.Fprintf(&b, "bank = selling.bank_container(mem, %d)\n", life)
	return b.String()
}

// Every slot of the bank reads the same, including the modifier that moved a
// price.
func TestReadingTheBankMatchesThePython(t *testing.T) {
	var want []map[string]any
	askPython(t, pyPlant(layout.BankSlots)+`print(json.dumps(bank.rows()))`, &want)

	bank, ok := selling.Bank(plant(layout.BankSlots), life)
	require.True(t, ok, "the bank did not read as one")

	got := bank.Rows()
	require.Len(t, got, len(want), "a different number of slots holds something")
	for i, w := range want {
		require.Equalf(t, w["index"], asJSON(t, got[i].Index), "slot %d is a different index", i)
		require.Equalf(t, w["addr"], asJSON(t, got[i].Addr), "slot %d is at a different address", i)
		require.Equalf(t, w["type"], asJSON(t, got[i].Type), "slot %d holds a different item", i)
		require.Equalf(t, w["stack"], asJSON(t, got[i].Stack), "slot %d holds a different count", i)
		require.Equalf(t, w["value"], asJSON(t, got[i].Value), "slot %d is worth something else", i)
		require.Equalf(t, w["favorited"], got[i].Favorited, "slot %d is favorited differently", i)
	}

	// The two copies of one item are priced differently, which is the whole
	// reason the price comes off the live item and not off its type's template.
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
		var want bool
		askPython(t, pyPlant(slots)+`print(json.dumps(bank is not None))`, &want)

		_, ok := selling.Bank(plant(slots), life)
		require.Equalf(t, want, ok, "a chest of %d slots is judged differently", slots)
		require.Equalf(t, slots == layout.BankSlots, ok,
			"a chest of %d slots was taken for the bank", slots)
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
			var want map[string]any
			askPython(t, pyPlant(layout.BankSlots)+fmt.Sprintf(`
ok = bank.write(0, expect_type=%d, expect_stack=%d, stack=0)
print(json.dumps({"ok": ok, "buf": mem.buf.hex()}))`, c.expectType, c.expectStack), &want)

			mem := plant(layout.BankSlots)
			bank, _ := selling.Bank(mem, life)
			got := bank.Write(0, selling.Change{
				ExpectType: c.expectType, ExpectStack: c.expectStack, Stack: &zero,
			})
			require.Equal(t, want["ok"], got, "the write was judged differently")
			require.Equal(t, c.ok, got, "and not as the case says")
			require.Equal(t, want["buf"], mem.Hex(), "the two left different memory behind")
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
