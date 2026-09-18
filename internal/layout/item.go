package layout

/*
An Item's fields.

Derived by diffing the game's own template objects against each other -- Copper
Pickaxe against Copper Axe to begin with -- rather than by dissecting the class.
docs/item-fields.md records each one and how it was confirmed.

Writing an unverified offset into an item is permanent, so a field nobody has
watched change is not here. Item.crit, Item.armorPenetration and
Item.bonusTagDamage were predicted together by declaration order; only crit was
ever observed moving (reforging a Minishark to Sighted took it 0 to 3, exactly
Sighted's bonus, while damage went 12 to 13), so only crit is written.
*/
const (
	ItemFishingPole = 0x58 // byte: the rod's fishing power
	ItemBait        = 0x5C // byte: the bait's power
	ItemType        = 0x6C
	/*
		ItemFavorited is set by alt-click, and the game uses it to protect a
		slot from quick-stack and from being sold.

		Derived live rather than by differencing templates, because a template
		is never favorited: three snapshots of the same six potions, favoriting
		and unfavoriting between them, and this is the byte that tracked the
		alt-clicks and nothing else. The rival reading was a "new item" glow
		flag, which looks identical at rest and is ruled out by the flag turning
		back on when a favorited item is clicked.
	*/
	ItemFavorited  = 0x70
	ItemAccessory  = 0x7D
	ItemUseAnim    = 0x80 // useAnimation, the visual swing
	ItemUseTime    = 0x84 // ticks per use; lower is faster
	ItemStack      = 0x88
	ItemPick       = 0x90 // pickaxe power, a percentage
	ItemAxe        = 0x94 // axe power; five times this is what the game shows
	ItemHammer     = 0x98 // hammer power, a percentage
	ItemTileBoost  = 0x9C // extra tiles of placement reach
	ItemDamage     = 0xAC // -1 for an item that does none
	ItemKnockback  = 0xB0 // float
	ItemHealLife   = 0xB4
	ItemHealMana   = 0xB8
	ItemConsumable = 0xBD // byte bool: used up on use
	ItemAutoReuse  = 0xBE // byte bool: swings while the button is held
	ItemScale      = 0xCC // float; size, which is also melee reach
	ItemDefense    = 0xD4
	ItemHeadSlot   = 0xD8
	ItemBodySlot   = 0xDC
	ItemLegSlot    = 0xE0
	ItemBuffType   = 0x130 // the BuffID granted on use
	ItemRare       = 0xF8  // -1 gray, 0 white, up to 10 red and 11 purple
	ItemShoot      = 0xFC  // the projectile type this item fires
	ItemShootSpeed = 0x100 // float
	ItemMana       = 0x11C // mana cost per use
	ItemCrit       = 0x150 // a modifier adds this rather than scaling it
	ItemPrefix     = 0x15C // byte: 0 none, then the modifier tier
	ItemMelee      = 0x15D // the four damage classes are byte bools and adjacent
	ItemMagic      = 0x15E
	ItemRanged     = 0x15F
	ItemSummon     = 0x160
)

// itemOffsets is the constants above under the Python's names for them.
var itemOffsets = map[string]int64{
	"ITEM_FISHING_POLE": ItemFishingPole,
	"ITEM_BAIT":         ItemBait,
	"ITEM_TYPE":         ItemType,
	"ITEM_FAVORITED":    ItemFavorited,
	"ITEM_ACCESSORY":    ItemAccessory,
	"ITEM_USE_ANIM":     ItemUseAnim,
	"ITEM_USE_TIME":     ItemUseTime,
	"ITEM_STACK":        ItemStack,
	"ITEM_PICK":         ItemPick,
	"ITEM_AXE":          ItemAxe,
	"ITEM_HAMMER":       ItemHammer,
	"ITEM_TILEBOOST":    ItemTileBoost,
	"ITEM_DAMAGE":       ItemDamage,
	"ITEM_KNOCKBACK":    ItemKnockback,
	"ITEM_HEAL_LIFE":    ItemHealLife,
	"ITEM_HEAL_MANA":    ItemHealMana,
	"ITEM_CONSUMABLE":   ItemConsumable,
	"ITEM_AUTOREUSE":    ItemAutoReuse,
	"ITEM_SCALE":        ItemScale,
	"ITEM_DEFENSE":      ItemDefense,
	"ITEM_HEAD_SLOT":    ItemHeadSlot,
	"ITEM_BODY_SLOT":    ItemBodySlot,
	"ITEM_LEG_SLOT":     ItemLegSlot,
	"ITEM_BUFF_TYPE":    ItemBuffType,
	"ITEM_RARE":         ItemRare,
	"ITEM_SHOOT":        ItemShoot,
	"ITEM_SHOOTSPEED":   ItemShootSpeed,
	"ITEM_MANA":         ItemMana,
	"ITEM_CRIT":         ItemCrit,
	"ITEM_PREFIX":       ItemPrefix,
	"ITEM_MELEE":        ItemMelee,
	"ITEM_MAGIC":        ItemMagic,
	"ITEM_RANGED":       ItemRanged,
	"ITEM_SUMMON":       ItemSummon,
}
