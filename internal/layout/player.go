package layout

/*
A player's fields, as offsets from Player.statLife.

statLife is the origin because it is what a scan finds: a number in a range
worth recognising, with everything else reached from it. Most of these are
negative for the same reason -- the fields are in front of it.

The six life and mana fields are contiguous and in declaration order, which is
why a block read of them is one read rather than six.
*/
const (
	StatLifeMax2Off = -0x08 // the permanent cap, from hearts
	StatLifeMaxOff  = -0x04 // the cap in effect, permanent plus temporary bonuses
	StatLifeOff     = 0x00  // current life, the field everything is measured from
	StatManaOff     = 0x04
	StatManaMaxOff  = 0x08
	StatManaMax2Off = 0x0C
	// NamePtrOff is Player.name, a mono String pointer. It is what tells one
	// player copy from another when the addresses mean nothing on their own.
	NamePtrOff = -0x6C0
	// InventoryPtrOff is the Item[] pointer.
	InventoryPtrOff = -0x664
	// InventorySlots is how many slots that array has: ten hotbar, forty main,
	// and the nine the game keeps behind them.
	InventorySlots = 59
	/*
		SelectedItemOff is Player.selectedItem, the hotbar index 0..9.

		Found by watching which ints track the hotbar and checking each against
		the slot the player was holding: it named Slime Whip, Boomstick and Book
		of Skulls correctly as they switched. A twin at statLife-0x690 never
		disagreed across 2473 samples; this is the lower of the pair.
	*/
	SelectedItemOff = -0x694
)

// playerOffsets is the constants above under the Python's names for them.
var playerOffsets = map[string]int64{
	"OFF_STAT_LIFE_MAX2": StatLifeMax2Off,
	"OFF_STAT_LIFE_MAX":  StatLifeMaxOff,
	"OFF_STAT_LIFE":      StatLifeOff,
	"OFF_STAT_MANA":      StatManaOff,
	"OFF_STAT_MANA_MAX":  StatManaMaxOff,
	"OFF_STAT_MANA_MAX2": StatManaMax2Off,
	"OFF_NAME_PTR":       NamePtrOff,
	"INVENTORY_PTR_OFF":  InventoryPtrOff,
	"INVENTORY_SLOTS":    InventorySlots,
	"SELECTED_ITEM_OFF":  SelectedItemOff,
}
