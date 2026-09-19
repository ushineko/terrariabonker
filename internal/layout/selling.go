package layout

/*
What selling reaches for: an item's worth, and the containers coins go into and
out of.

The offsets were asked of the mono runtime and then checked against live data,
because a runtime dump only proves the runtime agrees with itself.
*/
const (
	/*
		ItemValue is what an item is worth, held at **five times** its copper
		worth.

		The coins read 5, 500, 50000 and 5000000, so dividing by five returns
		exactly one of each. That is why the game's own sell path divides by
		five: the division is the sell rate itself.
	*/
	ItemValue = 0x124

	/*
		BankPtrOff is the Piggy Bank chest, and SafePtrOff the Safe beside it.

		Expressed as deltas from statLife, the way every inventory-side accessor
		in this project addresses a player, rather than from the object base.
		The Safe is kept for provenance and is not used as a fallback: a player
		who cannot reach their Piggy Bank cannot reach their Safe either.
	*/
	BankPtrOff = 0x0E0 - 0x738
	SafePtrOff = 0x0E4 - 0x738

	// ChestItemOff is the item array a chest holds.
	ChestItemOff = 0x08

	/*
		CopyLo and CopyHi bound the block copied out of a template when a slot's
		item is changed.

		It starts past the object header and reference pointers and covers the
		stat block; every item is the same large class, so this stays safely
		inside the object.
	*/
	CopyLo = 0x1C
	CopyHi = 0x140
)

/*
How many slots each container has.

BankSlots is the chest's own default, and it is **checked rather than trusted**
when a bank is opened: it is the cheapest signal that a chest pointer is really a
chest, and a wrong offset is far more likely to miss it than to hit it.

SellSlots stops one short of the inventory, because the last slot is not a normal
one and the grid hides it. CoinSlots is where coins may go, which the game's own
sell path bounds at the hotbar, the main grid and the coin slots.
*/
const (
	BankSlots = 40
	SellSlots = 58
	CoinSlots = 54
)

// sellingOffsets is those under the Python's names for them.
var sellingOffsets = map[string]int64{
	"ITEM_VALUE":     ItemValue,
	"BANK_PTR_OFF":   BankPtrOff,
	"SAFE_PTR_OFF":   SafePtrOff,
	"CHEST_ITEM_OFF": ChestItemOff,
	"COPY_LO":        CopyLo,
	"ITEM_COPY_HI":   CopyHi,
	"BANK_SLOTS":     BankSlots,
	"SELL_SLOTS":     SellSlots,
	"COIN_SLOTS":     CoinSlots,
}
