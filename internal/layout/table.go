package layout

import "strings"

/*
The version table: which game builds, under which .NET runtimes, this
program has numbers for (spec 052).

A supported version is a game build *and* the runtime executing it. The same
Terraria.exe gets a different object layout and different machine code under
wine-mono and under .NET Framework -- measured: not one field offset is shared
-- so numbers derived under one runtime say nothing about another. Each
combination with numbers of its own is one Entry, and the running combination
is matched against the table at startup.

The numbers themselves are still the package-level constants for the mono entry;
threading an Entry through every reader is spec 052 phase 3's next step. What
this table decides today is the part that cannot wait: whether the running
runtime is one any numbers here were derived for at all.
*/

// Family is a .NET runtime family.
type Family string

// The runtime families seen running the game.
const (
	// WineMono is wine-mono, under Proton on Linux.
	WineMono Family = "wine-mono"
	// NetFx is .NET Framework, on native Windows.
	NetFx Family = "netfx"
)

/*
Runtime is a detected runtime: its family and version, as version.DetectRuntime
spells them ("wine-mono-11.3.0", "netfx-4.8.9345.0").
*/
type Runtime struct {
	Family  Family
	Version string
}

// ParseRuntime reads a detected runtime string, and reports whether it named a
// family this table knows.
func ParseRuntime(s string) (Runtime, bool) {
	for _, f := range []Family{WineMono, NetFx} {
		if v, ok := strings.CutPrefix(s, string(f)+"-"); ok && v != "" {
			return Runtime{Family: f, Version: v}, true
		}
	}
	return Runtime{}, false
}

/*
Shapes is how a runtime lays out the objects every reader walks.

Measured per runtime, never inferred from the other: the CLR's array data was
expected at +0x0C for a reference-type array and is at +0x08.
*/
type Shapes struct {
	// ObjectHeader is how many bytes precede an object's first field.
	ObjectHeader int
	// StringLenOff and StringCharsOff locate a System.String's length and its
	// UTF-16 characters.
	StringLenOff, StringCharsOff int
	// ArrLenOff and ArrDataOff locate an szarray's length and first element.
	ArrLenOff, ArrDataOff int
	// LifeMaxFirst is whether statLifeMax is stored before statLifeMax2. The
	// six life and mana fields are contiguous, in declaration order, under both
	// runtimes (cmd/monofields and cmd/clrfields, 2026-10-08). It was once
	// thought to differ: the mono names had been swapped.
	LifeMaxFirst bool
}

// Feature is a part of the game an entry's numbers can read.
type Feature string

// The features an entry declares. A reader of one checks the entry first.
const (
	// ReadPlayer is finding the player and reading life and mana.
	ReadPlayer Feature = "player"
	// ReadInventory is reading the player's item slots.
	ReadInventory Feature = "inventory"
	// ReadLocalPlayer is ground truth for which player copy is live: Main.player
	// [Main.myPlayer], reached through the statics. Under mono it is found from
	// get_LocalPlayer's JIT code (locate.FindLocalPlayerAnchor), a mono byte
	// pattern.
	ReadLocalPlayer Feature = "local-player"
	// WritePlayerStats is writing life and mana: current values and caps.
	WritePlayerStats Feature = "player-stats"
	// WriteItemFields is editing an item's fields in place: stack, use speed,
	// pick power, reach, damage, auto-reuse. The item keeps its type.
	WriteItemFields Feature = "item-fields"
	// ReadTiles is reading the world tile map: ids and the active bit, for the
	// vein finder. A wrong layout reads wrong ids, never a write, so it is a read.
	ReadTiles Feature = "tiles"
	// ReadProjectiles is finding the projectile array and reading a fishing
	// bobber's state out of it (auto-catch). Read-only; a wrong layout reports no
	// bobber rather than acting.
	ReadProjectiles Feature = "projectiles"
	// WriteItemTemplates is putting a different item in a slot, or a modifier on
	// one: both copy from the game's pristine template of that item type, which
	// needs the template scan and the span of fields to copy.
	WriteItemTemplates Feature = "item-templates"
	// ReadContent is the compendium: scanning the heap for the game's pristine
	// item (and, where the entry allows, NPC) templates and reading their stats.
	// A read -- a wrong layout reads wrong stats, never a write.
	ReadContent Feature = "content"
	// ReadRecipes is walking Main.recipe[] and the two tables a station's name
	// is read through (extract-recipes, a maintenance command). A read -- a wrong
	// layout reads a wrong book, which is why the command asks.
	ReadRecipes Feature = "recipes"
	// WriteBuffs is renewing the player's active effects: writing a type and a
	// time into the two buff arrays (potions, fishing-buffs).
	WriteBuffs Feature = "buffs"
	// WriteFishingGear is the fishing command's own writes: a bait stack topped
	// up, a rod's fishing power raised or put back, and a rod and bait handed to
	// a player who has neither (the last through the item templates).
	WriteFishingGear Feature = "fishing-gear"
	// WriteSelling is auto-sell: coins credited to the Piggy Bank or the
	// inventory, then the sold stack cleared. It reads an item's value and reaches
	// the bank through the player, which are the entry's SellFields.
	WriteSelling Feature = "selling"
	// WriteProjectiles is the projectile editor: per-type overrides written onto
	// whatever is in flight, at the entry's ProjectileShape offsets. It covers the
	// lookup of which projectile an item fires, which serves nothing else.
	WriteProjectiles Feature = "projectile-edit"
	// WriteNPCSpawn is spawning an NPC: a template's fields copied over a free
	// Main.npc slot, at the entry's NPCShape and SpawnFields.
	WriteNPCSpawn Feature = "npc-spawn"
)

/*
ItemFields is where an Item object keeps the fields this program reads and writes,
object-relative. Byte-wide fields (the bools, prefix, fishing and bait power) are
noted; everything else is a 4-byte int or float.
*/
type ItemFields struct {
	Type, Stack, UseTime, UseAnim, Pick, TileBoost, Damage int
	Rare, Defense, BuffType, Mana, Crit                    int
	Knockback, Scale, ShootSpeed                           int // floats
	Shoot                                                  int // the projectile type fired
	FishingPole, Bait, Prefix                              int // bytes
	AutoReuse, Accessory, Favorited, Consumable            int // bools
	Melee, Magic, Ranged, Summon                           int // bools
	// CopyLo and CopyHi bound the block copied out of a pristine template when a
	// slot's item is changed: everything past the object header and the
	// reference fields, to the end of the object.
	CopyLo, CopyHi int
}

/*
ContentFields are the offsets the compendium's item/NPC template scan reads
beyond what ItemFields already carries, declared per runtime.

The item fields here are the ones only the catalog reads (a weapon's healing, an
armour slot), kept out of ItemFields so the editor's struct is not widened for
them. The NPC half of the scan takes its numbers from the entry's NPCShape.
*/
type ContentFields struct {
	CreateTile, HealLife, HealMana, HeadSlot, BodySlot, LegSlot int
}

/*
NPCShape is how one runtime lays an NPC object out and finds Main.npc: the
object-relative offsets of the fields the compendium reads from the game's NPC
templates, how far an NPC object reaches, and which resolver (content package)
finds the NPC array and the frame-count table. The szarray offsets are the
entry's Shapes. An entry with no resolver has no NPC stats: its catalog lists
NPCs by name.

Boss, Town and Active are one-byte bools; Color is four bytes; the rest are
4-byte ints.
*/
type NPCShape struct {
	Type, NetID, LifeMax, Damage, Defense int
	Width, Height                         int // Entity's
	Boss, Town, Active                    int // bools
	Color                                 int
	// Size is how much of an NPC object has to be readable before its fields can
	// be trusted: to the end of the furthest field.
	Size     int
	Resolver string // "mono" | "clr": how the array and the frame counts are found
}

/*
SpawnFields is what spawning an NPC writes beyond NPCShape: the Entity fields a
slot is given of its own, where the player faces, and which parts of a template
are copied.

CopySpans are half-open [lo, hi) ranges of an NPC object. Together they are the
whole object less its header, whoAmI, the four Vector2s from position to
oldVelocity, and every field holding a reference -- handing a slot the
template's arrays would make the two share them.
*/
type SpawnFields struct {
	// WhoAmI is Entity.whoAmI: the slot's own index in Main.npc.
	WhoAmI int
	// Position, Velocity and OldPosition are Entity's Vector2s, object-relative.
	Position, Velocity, OldPosition int
	// PlayerDirectionFromLife is the player's Entity.direction, -1 or 1, from
	// statLife.
	PlayerDirectionFromLife int
	CopySpans               [][2]int
}

/*
RecipeFields is what the recipe walk reads beyond ItemFields and ContentFields: a
Recipe's three fields, the style of the tile an item places, and where a localised
string keeps its text. Resolver names how Main.recipe and the two tile-name
statics are found (recipes' resolvers).
*/
type RecipeFields struct {
	// CreateItem and RequiredItem are references (an Item, an Item[]);
	// RequiredTile is an int. Object-relative.
	CreateItem, RequiredItem, RequiredTile int
	// PlaceStyle is Item.placeStyle, object-relative.
	PlaceStyle int
	// TextValue is LocalizedText._value, a string reference, object-relative.
	TextValue int
	Resolver  string
}

/*
SellFields is what selling reaches beyond ItemFields: what an item is worth, and
the Piggy Bank's item array through the player and its Chest.
*/
type SellFields struct {
	// ItemValue is Item.value, object-relative: five times the copper worth.
	ItemValue int
	// BankFromLife is Player.bank, a Chest reference, from statLife.
	BankFromLife int
	// ChestItem is Chest.item, the Item[] reference, object-relative.
	ChestItem int
}

/*
PlayerFields is where a player's parts are, measured from statLife: the field a
scan recognises, so everything else is reached from it.
*/
type PlayerFields struct {
	// NameFromLife is Player.name, a String pointer.
	NameFromLife int
	// InventoryFromLife is Player.inventory, the Item[] pointer.
	InventoryFromLife int
	// SelectedItemFromLife is the hotbar index the player holds, or 0 when the
	// entry has no measurement of it (statLife itself is never that field).
	SelectedItemFromLife int
	// The other five fields of the life and mana block, in the same order under
	// both runtimes measured (statLifeMax, the permanent cap, first).
	LifeMaxFromLife, LifeMax2FromLife               int
	ManaFromLife, ManaMaxFromLife, ManaMax2FromLife int
	// PositionFromLife is Entity.position (a Vector2, world pixels) from statLife.
	PositionFromLife int
	// BuffTypeFromLife and BuffTimeFromLife are the player's two buff-array
	// references (int[]), from statLife. Zero when the entry has no measurement;
	// the buff reader needs both.
	BuffTypeFromLife, BuffTimeFromLife int
}

// LocalPlayerBy is how an entry tells which player copy is the live one.
type LocalPlayerBy string

// The two ways.
const (
	// ByAnchor is mono's: get_LocalPlayer's JIT code leads to Main.player and
	// Main.myPlayer (locate.FindLocalPlayerAnchor).
	ByAnchor LocalPlayerBy = "anchor"
	// ByStatics is the CLR's: Main's reference statics block, recognised by the
	// three arrays it points to, and the active element of Main.player.
	ByStatics LocalPlayerBy = "statics"
)

/*
MainStatics is how to recognise Main's reference statics block and read the live
player through it, under an entry that finds it ByStatics.

The block is recognised by identity rather than located by an address: the slot
that holds Main.player also has Main.npc and Main.projectile beside it at fixed
distances, and those three arrays have the game's own lengths. Offsets are
relative to the Main.player slot.
*/
type MainStatics struct {
	NPCFromPlayer, ProjectileFromPlayer int
	PlayerLen, NPCLen, ProjectileLen    uint32
	// PlayerActive is Player.active, a bool, object-relative. In single-player
	// the other slots hold inactive placeholder players.
	PlayerActive int
	// LifeInPlayer is Player.statLife, object-relative.
	LifeInPlayer int
	// TileFromPlayer is Main.tile's static slot relative to Main.player's, for an
	// entry that finds the tile array by the statics (the CLR). Zero when the tile
	// array is reached another way (mono, via a static-block offset).
	TileFromPlayer int
	// NPCFrameCountFromPlayer and WorldNameFromPlayer are Main.npcFrameCount's and
	// Main.worldName's static slots relative to Main.player's, on the same terms
	// as TileFromPlayer: zero when the entry reaches them through a static block.
	NPCFrameCountFromPlayer, WorldNameFromPlayer int
	// RecipeFromPlayer is Main.recipe's slot on the same terms. TileLookupFromPlayer
	// and MapLegendFromPlayer are MapHelper.tileLookup's and Lang._mapLegendCache's:
	// other classes' statics, which the CLR keeps in the same block as Main's, at
	// offsets counted across the whole module. Zero when the entry finds them
	// another way (mono, from the code that loads them).
	RecipeFromPlayer, TileLookupFromPlayer, MapLegendFromPlayer int
}

/*
TileShape is how one runtime lays the world tiles out: the offsets of a tile
object's fields, the tile objects' allocation stride, where an array object keeps
its first element, and which resolver (tiles package) finds the array and its
dimensions. Tiles are a reference array on both runtimes; only these numbers and
the resolver differ.
*/
type TileShape struct {
	TypeOff, HeaderOff int    // within a Tile object
	ActiveBit          uint16 // active() is (sTileHeader & ActiveBit) != 0
	Record             int    // the tile objects' contiguous allocation stride
	DataOff            int    // the array object's first element
	Resolver           string // "mono" | "clr": how the array and dims are found
}

/*
ProjectileShape is how one runtime lays a Projectile out and finds the projectile
array: the object-relative offsets of the two float-array references the bite
condition reads (ai, localAI) and the two one-byte flags (active, bobber), the
fields the projectile editor writes, how many projectiles the game keeps, and
which resolver (projectile package) finds the array. The szarray offsets (length,
first element) are the entry's Shapes. Only these numbers and the resolver differ
between runtimes.
*/
type ProjectileShape struct {
	AIOff, LocalAIOff    int    // -> float[3] field references, object-relative
	ActiveOff, BobberOff int    // one-byte bools, object-relative
	Len                  int    // how many projectiles the array holds
	Resolver             string // "mono" | "clr": how the array is found
	// What the editor reads and writes: type, timeLeft, penetrate, maxPenetrate
	// and extraUpdates are 4-byte ints, scale a float, tileCollide a one-byte bool
	// with other bools packed against it.
	TypeOff, TimeLeftOff, PenetrateOff, MaxPenetrateOff, ExtraUpdatesOff int
	ScaleOff                                                             int
	TileCollideOff                                                       int
}

/*
Entry is one supported combination: a runtime family, the builds and runtime
versions its numbers were derived or confirmed on, what it can read, and whether
it may write.

Reads and Writes are separate because they become true at different times. An
entry's reads land one feature at a time, and each is harmless if wrong -- a
locator that validates by name finds nothing. A write with a wrong number lands
in the wrong field of a live save, so Writes stays false until every write path
takes its numbers from the entry.
*/
type Entry struct {
	Name   string
	Family Family
	// Builds are the game build keys (version.BuildKey) the numbers fit.
	Builds []string
	// Versions are the runtime versions Select accepts as an exact match. Nil
	// means any version of the family; see monoEntry for why that exists.
	Versions []string
	// Confirmed is a ledger, not a gate: runtime versions the numbers were checked
	// against a live game under, with what was checked. Select does not read it.
	Confirmed []string
	// Enabled is whether Select may choose the entry at all. An entry can be
	// measured before any reader uses it.
	Enabled bool
	// Reads is the features this entry's numbers can read.
	Reads []Feature
	// Writes is whether this program may make every write under the entry.
	Writes bool
	// WriteFeatures are the writes allowed when Writes is false: an entry whose
	// write paths land one feature at a time, as its reads do.
	WriteFeatures []Feature
	Shapes        Shapes
	Player        PlayerFields
	Item          ItemFields
	// Content is the extra offsets the compendium's template scan reads.
	Content ContentFields
	// NPC is how NPC objects are laid out and Main.npc found.
	NPC NPCShape
	// Spawn is what spawning an NPC writes beyond NPC.
	Spawn SpawnFields
	// Selling is what auto-sell reaches beyond Item.
	Selling SellFields
	// Recipes is what the recipe walk reaches beyond Item and Content.
	Recipes RecipeFields
	// LocalPlayer is how ReadLocalPlayer is done, and Statics its numbers when it
	// is ByStatics.
	LocalPlayer LocalPlayerBy
	Statics     MainStatics
	// PlayerValues are player fields a cheat sets alongside its patch, by the
	// game's field name, as offsets from statLife.
	PlayerValues map[string]int
	// Anchors and Cheats are the in-place code patches under this entry
	// (patches.go).
	Anchors map[string]AnchorDef
	Cheats  map[string]CheatSite
	// Injections names the injection set (patch's registry) this entry's
	// stub-based cheats come from; "" when it has none.
	Injections string
	// Tiles is how the world tiles are laid out and found.
	Tiles TileShape
	// Projectiles is how projectiles are laid out and the array found.
	Projectiles ProjectileShape
	// Provenance is where the numbers came from.
	Provenance string
}

// Entries is the table, in the order a family's candidates are tried.
var Entries = []Entry{monoEntry, clrEntry}

// Support is how the running build and runtime stand against the table.
type Support string

// The answers Select gives.
const (
	// Supported: an enabled entry names this build and this runtime version.
	Supported Support = "supported"
	// Candidate: an enabled entry exists for this runtime family, but not for
	// this exact build or runtime version. The existing degraded-build flow
	// decides, as it does for a game update today.
	Candidate Support = "candidate"
	// Unsupported: no enabled entry for this runtime family. The numbers here
	// are known not to fit, so memory work is refused.
	Unsupported Support = "unsupported"
	// RuntimeUnknown: the runtime could not be detected. Not evidence of
	// anything -- the same rule as an unreadable game version -- so the mono
	// entry is used, which is what happened before the table existed. On Windows
	// the runtime is never undetected while the CLR is loaded: version reports
	// netfx-unknown, which is the NetFx family.
	RuntimeUnknown Support = "unknown"
)

/*
Select is the entry for a running build and runtime, and how well it fits.

The entry returned for Candidate is the family's first enabled one, which is
the nearest the table has. For RuntimeUnknown it is the mono entry. For
Unsupported it is the zero Entry, which can read nothing and write nothing.
*/
func Select(build, runtime string) (Entry, Support) {
	if runtime == "" {
		return monoEntry, RuntimeUnknown
	}
	// A runtime that was detected and is not one this table knows is not
	// "unknown": something is running the game, and no numbers here were derived
	// for it.
	rt, ok := ParseRuntime(runtime)
	if !ok {
		return Entry{}, Unsupported
	}
	var nearest *Entry
	for i := range Entries {
		e := &Entries[i]
		if e.Family != rt.Family || !e.Enabled {
			continue
		}
		if nearest == nil {
			nearest = e
		}
		if has(e.Builds, build) && (e.Versions == nil || has(e.Versions, rt.Version)) {
			return *e, Supported
		}
	}
	if nearest == nil {
		return Entry{}, Unsupported
	}
	return *nearest, Candidate
}

// CanRead reports whether the entry's numbers can read a feature.
func (e Entry) CanRead(f Feature) bool { return e.Enabled && has(e.Reads, f) }

func has[T comparable](list []T, want T) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Mono is the wine-mono entry: the numbers every reader used before the table,
// and still the ones a reader uses when not handed an entry.
func Mono() Entry { return monoEntry }

// CanWrite reports whether a write of a feature is allowed under the entry.
func (e Entry) CanWrite(f Feature) bool {
	return e.Enabled && (e.Writes || has(e.WriteFeatures, f))
}
