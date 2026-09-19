package layout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Every offset this project declares, frozen.

An offset is a number somebody derived from a running game, and it is the kind
of thing that gets "tidied" -- a field renamed, a constant moved, a value copied
into a second place and then only one of them corrected. None of that fails
visibly: a wrong offset reads a neighbouring field, and the number that comes
back looks like a number.

So the whole table is written out here. Changing one is then two edits in two
files, which is what makes it deliberate. A game update is the routine event in
this project and re-deriving an offset is expected -- what is not expected is an
offset changing because of a refactor.

This replaces a comparison against the Python implementation these were ported
from, which is gone. The values are the ones that comparison agreed on.
*/
var goldenOffsets = map[string]int64{
	"ACTIVE_OFF":               120,
	"AI_OFF":                   68,
	"ARRAY_LEN":                1001,
	"ARR_DATA_OFF":             16,
	"ARR_LEN_OFF":              12,
	"BANK_PTR_OFF":             -1624,
	"BANK_SLOTS":               40,
	"BOBBER_OFF":               136,
	"BUFF_TIME_PTR_OFF":        -1644,
	"BUFF_TYPE_PTR_OFF":        -1648,
	"CHEST_ITEM_OFF":           8,
	"COIN_SLOTS":               54,
	"COPY_LO":                  28,
	"EXTRAUPDATES_OFF":         260,
	"INVENTORY_PTR_OFF":        -1636,
	"INVENTORY_SLOTS":          59,
	"ITEM_ACCESSORY":           125,
	"ITEM_AUTOREUSE":           190,
	"ITEM_AXE":                 148,
	"ITEM_BAIT":                92,
	"ITEM_BODY_SLOT":           220,
	"ITEM_BUFF_TYPE":           304,
	"ITEM_CONSUMABLE":          189,
	"ITEM_COPY_HI":             320,
	"ITEM_CREATE_TILE":         160,
	"ITEM_CRIT":                336,
	"ITEM_DAMAGE":              172,
	"ITEM_DEFENSE":             212,
	"ITEM_FAVORITED":           112,
	"ITEM_FISHING_POLE":        88,
	"ITEM_HAMMER":              152,
	"ITEM_HEAD_SLOT":           216,
	"ITEM_HEAL_LIFE":           180,
	"ITEM_HEAL_MANA":           184,
	"ITEM_KNOCKBACK":           176,
	"ITEM_LEG_SLOT":            224,
	"ITEM_MAGIC":               350,
	"ITEM_MANA":                284,
	"ITEM_MELEE":               349,
	"ITEM_PICK":                144,
	"ITEM_PLACE_STYLE":         168,
	"ITEM_PREFIX":              348,
	"ITEM_RANGED":              351,
	"ITEM_RARE":                248,
	"ITEM_SCALE":               204,
	"ITEM_SHOOT":               252,
	"ITEM_SHOOTSPEED":          256,
	"ITEM_STACK":               136,
	"ITEM_SUMMON":              352,
	"ITEM_TILEBOOST":           156,
	"ITEM_TYPE":                108,
	"ITEM_USE_ANIM":            128,
	"ITEM_USE_TIME":            132,
	"ITEM_VALUE":               292,
	"LOCALAI_OFF":              72,
	"LOCALIZEDTEXT_VALUE":      12,
	"MAIN_MAX_TILES_OFF":       1444,
	"MAIN_NPC_FRAME_COUNT_OFF": 3124,
	"MAIN_NPC_OFF":             2480,
	"MAIN_PLAYER_OFF":          2684,
	"MAIN_PROJECTILE_OFF":      2492,
	"MAIN_RECIPE_OFF":          2664,
	"MAIN_TILE_OFF":            2460,
	"MAIN_WORLD_NAME_OFF":      1632,
	"MAXPENETRATE_OFF":         220,
	"MAX_NPCS":                 200,
	"MAX_NPC_FRAMES":           64,
	"MAX_NPC_TYPE":             2000,
	"NPC_ACTIVE":               459,
	"NPC_BOSS":                 460,
	"NPC_COLOR":                424,
	"NPC_DAMAGE":               352,
	"NPC_DEFENSE":              356,
	"NPC_HEIGHT":               56,
	"NPC_LIFE_MAX":             368,
	"NPC_NET_ID":               488,
	"NPC_OBJECT_SIZE":          664,
	"NPC_OLD_POSITION_X":       28,
	"NPC_POSITION_X":           12,
	"NPC_POSITION_Y":           16,
	"NPC_TOWN":                 532,
	"NPC_TYPE":                 320,
	"NPC_VELOCITY_X":           20,
	"NPC_WHO_AMI":              8,
	"NPC_WIDTH":                52,
	"OFF_NAME_PTR":             -1728,
	"OFF_STAT_LIFE":            0,
	"OFF_STAT_LIFE_MAX":        -4,
	"OFF_STAT_LIFE_MAX2":       -8,
	"OFF_STAT_MANA":            4,
	"OFF_STAT_MANA_MAX":        8,
	"OFF_STAT_MANA_MAX2":       12,
	"PENETRATE_OFF":            212,
	"RECIPE_CREATE_ITEM":       8,
	"RECIPE_REQUIRED_ITEM":     12,
	"RECIPE_REQUIRED_TILE":     28,
	"SAFE_PTR_OFF":             -1620,
	"SCALE_OFF":                140,
	"SELECTED_ITEM_OFF":        -1684,
	"SELL_SLOTS":               58,
	"TILECOLLIDE_OFF":          256,
	"TIMELEFT_OFF":             180,
	"TYPE_OFF":                 148,
	"WET_OFF":                  60,
}

/*
TestEveryOffsetIsWhatItWas compares the table both ways.

Both directions, because the two failures are different: an offset whose value
changed is a field being read in the wrong place, and an offset that appeared or
vanished is a number declared somewhere this file does not know about -- which
is the duplication this package exists to prevent.
*/
func TestEveryOffsetIsWhatItWas(t *testing.T) {
	for name, want := range goldenOffsets {
		got, known := layout.Offsets[name]
		require.Truef(t, known, "%s is no longer declared", name)
		require.Equalf(t, want, got, "%s has changed", name)
	}
	for name := range layout.Offsets {
		_, known := goldenOffsets[name]
		require.Truef(t, known, "%s is new and is not written down here", name)
	}
}

/*
And the copy spans, which are not numbers and so are not in the table above.

A span is exactly the kind of thing that gets widened on one side only, because
widening it looks harmless until a spawned NPC shares an array with the template
it was copied from.
*/
func TestTheCopySpansAreWhatTheyWere(t *testing.T) {
	require.Equal(t, [][2]int{{0x2C, 0x44}, {0x70, 0x298}}, layout.NPCCopySpans)
}
