package layout

/*
A player's fields, as offsets from Player.statLife.

statLife is the origin because it is what a scan finds: a number in a range
worth recognising, with everything else reached from it. Most of these are
negative for the same reason -- the fields are in front of it.

The six life and mana fields are contiguous and in declaration order, which is
why a block read of them is one read rather than six.

The names are the game's, as the mono runtime reports them (cmd/monofields
against the live game, 2026-10-08: statLifeMax 0x730, statLifeMax2 0x734,
statLife 0x738). statLifeMax is the permanent cap, from Life Crystals and Life
Fruit; statLifeMax2 is the cap in effect, after accessories and buffs. Until then
the two names here were swapped -- inferred, never asked -- while the comments
beside them were right, and ValidBlock, trusting the names, took the permanent cap
for the boosted one: a player whose cap in effect was above the permanent one
was never found.
*/
const (
	StatLifeMaxOff  = -0x08 // statLifeMax: the permanent cap, from hearts
	StatLifeMax2Off = -0x04 // statLifeMax2: the cap in effect, permanent plus bonuses
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
	/*
		PickSpeedOff and BlockRangeOff are the fields the mining and reach cheats
		set alongside their code patches: Player.pickSpeed (a float, lower mines
		faster) and Player.blockRange (extra tiles of placement reach). Cheat
		Engine's mono dissector put them at 0x8D8 and 0x9F8 against statLife at
		0x738 (ce/README.md). Until 2026-10-09 they were bare numbers in
		patch/cheats.go.
	*/
	PickSpeedOff  = 0x1A0
	BlockRangeOff = 0x2C0
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
	"PICK_SPEED_OFF":     PickSpeedOff,
	"BLOCK_RANGE_OFF":    BlockRangeOff,
}
