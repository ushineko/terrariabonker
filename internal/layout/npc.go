package layout

/*
An NPC's fields, as offsets into the NPC object.

Read from the game's own template objects rather than from the NPCs in the world:
a live NPC's stats have been scaled by the world's difficulty, so a Blue Slime in
an expert world reads 60 life where its template says 25.
*/
const (
	NPCWhoAmI       = 0x008
	NPCPositionX    = 0x00C
	NPCPositionY    = 0x010
	NPCVelocityX    = 0x014
	NPCOldPositionX = 0x01C
	NPCWidth        = 0x034
	NPCHeight       = 0x038
	NPCType         = 0x140
	NPCDamage       = 0x160
	NPCDefense      = 0x164
	NPCLifeMax      = 0x170
	NPCColor        = 0x1A8 // the tint a neutral sheet is painted with; zero means none
	NPCActive       = 0x1CB
	NPCBoss         = 0x1CC
	/*
		NPCNetID tells the variants apart.

		The coloured slimes and their kind share a type and differ only by a
		negative netID, and those are the names the bundled table carries -- which
		is why the templates are keyed on it.
	*/
	NPCNetID = 0x1E8
	NPCTown  = 0x214
	// NPCObjectSize is how far an NPC object reaches, which is how much of one
	// has to be readable before its fields can be trusted.
	NPCObjectSize = 664
)

// The bounds an NPC is judged against: a type or netID outside these is a stale
// pointer read as an object, not an NPC.
const (
	MaxNPCType   = 2000
	MaxNPCs      = 200
	MaxNPCFrames = 64
)

// npcOffsets is the constants above under the Python's names for them.
var npcOffsets = map[string]int64{
	"NPC_WHO_AMI":        NPCWhoAmI,
	"NPC_POSITION_X":     NPCPositionX,
	"NPC_POSITION_Y":     NPCPositionY,
	"NPC_VELOCITY_X":     NPCVelocityX,
	"NPC_OLD_POSITION_X": NPCOldPositionX,
	"NPC_WIDTH":          NPCWidth,
	"NPC_HEIGHT":         NPCHeight,
	"NPC_TYPE":           NPCType,
	"NPC_DAMAGE":         NPCDamage,
	"NPC_DEFENSE":        NPCDefense,
	"NPC_LIFE_MAX":       NPCLifeMax,
	"NPC_COLOR":          NPCColor,
	"NPC_ACTIVE":         NPCActive,
	"NPC_BOSS":           NPCBoss,
	"NPC_NET_ID":         NPCNetID,
	"NPC_TOWN":           NPCTown,
	"NPC_OBJECT_SIZE":    NPCObjectSize,
	"MAX_NPC_TYPE":       MaxNPCType,
	"MAX_NPCS":           MaxNPCs,
	"MAX_NPC_FRAMES":     MaxNPCFrames,
}

/*
A recipe's fields, and the two item fields only the recipe reader needs.

ItemCreateTile is what an item places, which is also how the compendium tells a
block from a material.
*/
const (
	RecipeCreateItem   = 0x08
	RecipeRequiredItem = 0x0C
	RecipeRequiredTile = 0x1C
	ItemCreateTile     = 0xA0
	ItemPlaceStyle     = 0xA8
	// LocalizedTextValue is where a localised string keeps the text itself,
	// which is how a station's name is read rather than its key.
	LocalizedTextValue = 0x0C
)

// recipeOffsets is those, again under the Python's names.
var recipeOffsets = map[string]int64{
	"RECIPE_CREATE_ITEM":   RecipeCreateItem,
	"RECIPE_REQUIRED_ITEM": RecipeRequiredItem,
	"RECIPE_REQUIRED_TILE": RecipeRequiredTile,
	"ITEM_CREATE_TILE":     ItemCreateTile,
	"ITEM_PLACE_STYLE":     ItemPlaceStyle,
	"LOCALIZEDTEXT_VALUE":  LocalizedTextValue,
}
