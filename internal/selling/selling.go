/*
Package selling prices a whitelisted item, takes it, and pays for it in coins.

The game's own sell path is deliberately **not** called. It takes no shopkeeper
argument and checks none -- that gate lives entirely in the interface -- so the
whole transaction is "clear a slot, credit some coins", which is arithmetic this
package can do without injecting anything into the game.

Two facts here were measured rather than reasoned, and both would have been got
wrong. An item's value is held at five times its copper worth, which is why the
game divides by five: the division is the sell rate. And **a modifier scales that
value** -- three of one staff in a single inventory read 100000, 189062 and 58522
-- so pricing from the item type's template, the obvious implementation, would
mis-price every prefixed item. The price is always read off the live item.

Ported from terrariabonker/selling.py (spec 051, step 5).
*/
package selling

import (
	"encoding/binary"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
The game's own constants.

The four coins, what one of each is worth, and the point at which the game
promotes a stack to the next denomination up.
*/
var (
	CoinTypes = [4]int32{71, 72, 73, 74}
	CoinWorth = [4]int32{1, 100, 10_000, 1_000_000}
)

const (
	CoinMaxStack = 100 //nolint:revive // documented by the comment on the line // a hundred of a coin becomes one of the next tier up
	SellDivisor  = 5

	// PiggyBankItem is placeable anywhere, so carrying one makes the bank
	// reachable; the trough summons a flying one that opens the same container.
	PiggyBankItem   = 87
	MoneyTroughItem = 3213
	PiggyBankTile   = 29
)

/*
Price is what a stack of an item sells for, in copper.

The per-unit price is floored at one *before* being multiplied by the stack, so a
thousand near-worthless items are worth a thousand copper rather than nothing.
*/
func Price(value, stack int32) int32 {
	if value <= 0 || stack <= 0 {
		return 0
	}
	per := value / SellDivisor
	if per < 1 {
		per = 1
	}
	return per * stack
}

// Coins is one coin denomination and how many of it.
type Coins struct {
	Type  int32 `json:"type"`
	Stack int32 `json:"stack"`
}

/*
CoinStacks is an amount of copper split into denominations, largest first.

This is the shape the game leaves an inventory in: nothing below a hundred of a
denomination that could have been carried by the next one up.
*/
func CoinStacks(copper int32) []Coins {
	var out []Coins
	if copper <= 0 {
		return out
	}
	for i := len(CoinTypes) - 1; i >= 0; i-- {
		n := copper / CoinWorth[i]
		copper -= n * CoinWorth[i]
		if n > 0 {
			out = append(out, Coins{Type: CoinTypes[i], Stack: n})
		}
	}
	return out
}

// Mem is the memory a container is read from and written to.
type Mem interface {
	Read(addr uint32, size int) []byte
	Write(addr uint32, data []byte) bool
	ReadU32(addr uint32) (uint32, bool)
	ReadI32(addr uint32) (int32, bool)
	WriteI32(addr uint32, value int32) bool
}

/*
Container is a read and write view of one item array -- the inventory, or a bank
chest.

Addressed by *identity on every access* rather than by a cached array pointer:
mono moves objects, and a stale pointer writes into whatever now lives there.
*/
type Container struct {
	Mem   Mem
	Array func() (uint32, bool)
	Slots int
}

// ItemAddr is the item object in a slot, or nothing when there is none.
func (c *Container) ItemAddr(index int) (uint32, bool) {
	arr, ok := c.Array()
	if !ok || index < 0 || index >= c.Slots {
		return 0, false
	}
	addr, ok := c.Mem.ReadU32(arr + layout.ArrDataOff + uint32(index)*4) //nolint:gosec // a slot index
	return addr, ok && addr != 0
}

// Row is one slot as the seller sees it.
type Row struct {
	Index     int    `json:"index"`
	Addr      uint32 `json:"addr"`
	Type      int32  `json:"type"`
	Stack     int32  `json:"stack"`
	Favorited bool   `json:"favorited"`
	Value     int32  `json:"value"`
}

// Read is one slot, and whether it could be read at all.
func (c *Container) Read(index int) (Row, bool) {
	addr, ok := c.ItemAddr(index)
	if !ok {
		return Row{}, false
	}
	// One read covering type through stack, which is where everything but the
	// value lives.
	width := layout.ItemStack - layout.ItemType + 4
	head := c.Mem.Read(addr+uint32(layout.ItemType), width) //nolint:gosec // a field offset
	if len(head) < width {
		return Row{}, false
	}
	value, _ := c.Mem.ReadI32(addr + layout.ItemValue)
	return Row{
		Index: index, Addr: addr,
		Type:      int32(binary.LittleEndian.Uint32(head)),                                    //nolint:gosec // a field, as its bits
		Stack:     int32(binary.LittleEndian.Uint32(head[layout.ItemStack-layout.ItemType:])), //nolint:gosec // a field, as its bits
		Favorited: head[layout.ItemFavorited-layout.ItemType] != 0,
		Value:     value,
	}, true
}

// Rows is every slot that holds something readable.
func (c *Container) Rows() []Row {
	out := make([]Row, 0, c.Slots)
	for i := range c.Slots {
		if row, ok := c.Read(i); ok {
			out = append(out, row)
		}
	}
	return out
}

// Change is what to write into a slot. A field left nil is left alone.
type Change struct {
	// ExpectType and ExpectStack are what the caller believed was in the slot.
	ExpectType  int32
	ExpectStack int32

	Stack    *int32
	ItemType *int32
	Block    []byte
}

/*
Write puts a change into one slot, re-resolving its address and checking what is
there first.

**Never write through an address read earlier.** mono moves objects, so a pointer
captured a few syscalls ago can name memory that now belongs to something else --
and a round that reads forty slots and then writes to them is a wide window.
Opening a chest is exactly the kind of allocation that moves things.

A mismatch against what the caller expected means the slot changed underneath, which
is a reason to stop rather than a value to overwrite.
*/
func (c *Container) Write(index int, change Change) bool {
	addr, ok := c.ItemAddr(index) // resolved now, not earlier
	if !ok {
		return false
	}
	if got, _ := c.Mem.ReadI32(addr + uint32(layout.ItemType)); got != change.ExpectType { //nolint:gosec // a field offset
		return false
	}
	if got, _ := c.Mem.ReadI32(addr + uint32(layout.ItemStack)); got != change.ExpectStack { //nolint:gosec // a field offset
		return false
	}
	switch {
	case change.Block != nil:
		c.Mem.Write(addr+layout.CopyLo, change.Block)
	case change.ItemType != nil:
		c.Mem.WriteI32(addr+uint32(layout.ItemType), *change.ItemType) //nolint:gosec // a field offset
	}
	if change.Stack != nil {
		c.Mem.WriteI32(addr+uint32(layout.ItemStack), *change.Stack) //nolint:gosec // a field offset
	}
	return true
}

/*
Bank is the player's Piggy Bank, and whether it reads as one.

The forty-slot length is **checked rather than trusted**: it is the cheapest
signal that the chest pointer is really a chest, and a wrong offset is far more
likely to miss it than to hit it.
*/
func Bank(mem Mem, lifeAddr uint32) (*Container, bool) {
	array := func() (uint32, bool) {
		chest, ok := mem.ReadU32(uint32(int64(lifeAddr) + layout.BankPtrOff)) //nolint:gosec // a delta from statLife
		if !ok || chest == 0 {
			return 0, false
		}
		arr, ok := mem.ReadU32(chest + layout.ChestItemOff)
		if !ok || arr == 0 {
			return 0, false
		}
		if n, _ := mem.ReadI32(arr + layout.ArrLenOff); n != layout.BankSlots {
			return 0, false
		}
		return arr, true
	}
	if _, ok := array(); !ok {
		return nil, false
	}
	return &Container{Mem: mem, Array: array, Slots: layout.BankSlots}, true
}
