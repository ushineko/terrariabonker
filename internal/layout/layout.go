/*
Package layout is the game's memory layout: mono's array shape and Main's
static offsets.

Every number here is build-specific and has to be re-derived when the game
updates, which is the routine event this project is built around.

The Python module this mirrors exists because these numbers were once spelled
five times under four names across five files, so re-deriving one meant finding
every spelling of it first. Porting them makes a second home for them, which is
the failure that module was created to end -- so the two are not left to agree by
inspection: a test asks the Python for every constant it has and compares the
whole set, by name and by value. A number added on one side and missed on the
other is a failing test rather than a silent difference.

See docs/discovery.md for how each was found, and the build ledger in patcher for
which builds each has been seen working on.
*/
package layout

/*
The mono szarray, 32 bits.

An array object is a vtable, a sync block, a bounds pointer, then the length;
elements start at 0x10. The length sits at 0x0C and not at 0x08 -- a first pass
at the projectile array used 0x08 and concluded the structure was not there.
*/
const (
	ArrLenOff  = 0x0C // max_length
	ArrDataOff = 0x10 // the first element
)

/*
Terraria.Main's static data block, as offsets from the base that
locate.MainStaticBase resolves.

MainWorldNameOff is the only thing in the block that says which world is loaded,
and it was found the hard way: by diffing the static block across a real world
switch, where 139 dwords changed and this was the one whose value matched the
world files either side. The tile buffer's address and the world dimensions do
*not* identify a world -- both were byte-identical across a switch between two
worlds of the same size.
*/
const (
	MainMaxTilesOff      = 0x5A4 // maxTilesX, then maxTilesY
	MainWorldNameOff     = 0x660 // Main.worldName, a mono string
	MainTileOff          = 0x99C
	MainNPCOff           = 0x9B0
	MainProjectileOff    = 0x9BC
	MainRecipeOff        = 0xA68
	MainPlayerOff        = 0xA7C
	MainNPCFrameCountOff = 0xC34
)

/*
Offsets is every constant above, under the name the Python gives it.

It exists for the test that compares the two, and it is written next to the
constants it names so that adding one and forgetting the other is visible where
it happens. The values are the constants themselves, so a number cannot disagree
with its own entry.
*/
var Offsets = map[string]uint32{
	"ARR_LEN_OFF":              ArrLenOff,
	"ARR_DATA_OFF":             ArrDataOff,
	"MAIN_MAX_TILES_OFF":       MainMaxTilesOff,
	"MAIN_WORLD_NAME_OFF":      MainWorldNameOff,
	"MAIN_TILE_OFF":            MainTileOff,
	"MAIN_NPC_OFF":             MainNPCOff,
	"MAIN_PROJECTILE_OFF":      MainProjectileOff,
	"MAIN_RECIPE_OFF":          MainRecipeOff,
	"MAIN_PLAYER_OFF":          MainPlayerOff,
	"MAIN_NPC_FRAME_COUNT_OFF": MainNPCFrameCountOff,
}
