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
	// six life and mana fields are contiguous under both runtimes, in a
	// different order.
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
)

/*
PlayerFields is where a player's parts are, measured from statLife: the field a
scan recognises, so everything else is reached from it.
*/
type PlayerFields struct {
	// NameFromLife is Player.name, a String pointer.
	NameFromLife int
	// InventoryFromLife is Player.inventory, the Item[] pointer.
	InventoryFromLife int
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
	// Writes is whether this program may write game memory under the entry.
	Writes bool
	Shapes Shapes
	Player PlayerFields
	// LocalPlayer is how ReadLocalPlayer is done, and Statics its numbers when it
	// is ByStatics.
	LocalPlayer LocalPlayerBy
	Statics     MainStatics
	// Provenance is where the numbers came from.
	Provenance string
}

/*
monoEntry is the numbers this project has always had: the package-level
constants, derived under wine-mono with Cheat Engine's mono dissector and
cmd/monofields.

Versions is nil -- any wine-mono -- because the version they were first verified
under was never recorded: the accepted-builds ledger predates runtime tracking,
and Proton Experimental has updated wine-mono since (11.3.0 as of 2026-10-08;
10.4.1 and 11.2.0 are also installed on the maintainer's machine). Nil keeps
today's behaviour, which accepted every wine-mono.

11.3.0 has since been checked (Confirmed). Versions stays nil all the same:
narrowing it would send every GE-Proton (10.4.1) and proton-cachyos (11.2.0)
user to the "Terraria has updated" question on no evidence that their runtime
differs. When a wine-mono version is measured to need different numbers, it gets
an entry of its own.
*/
var monoEntry = Entry{
	Name:   "wine-mono",
	Family: WineMono,
	Builds: []string{"1.4.5.7+24825745", "1.4.5.8+24893155"},
	Confirmed: []string{
		"11.3.0: 1.4.5.8+24893155 in a world, 2026-10-08 -- the player located by name, " +
			"stats and inventory read, all 15 cheat anchors resolved, and the maintainer " +
			"confirmed the cheats still work in play",
	},
	Shapes: Shapes{
		ObjectHeader: 0x08,
		StringLenOff: 0x08, StringCharsOff: 0x0C,
		ArrLenOff: ArrLenOff, ArrDataOff: ArrDataOff,
		LifeMaxFirst: false,
	},
	Player:      PlayerFields{NameFromLife: NamePtrOff, InventoryFromLife: InventoryPtrOff},
	Reads:       []Feature{ReadPlayer, ReadInventory, ReadLocalPlayer},
	LocalPlayer: ByAnchor,
	Writes:      true,
	Enabled:     true,
	Provenance:  "Cheat Engine mono dissector and cmd/monofields, 1.4.5.7 and 1.4.5.8",
}

/*
clrEntry is .NET Framework on native Windows, measured in spec 052 phase 0.

Enabled for reading the player and which copy is live, and never for writing yet: the CLR read path
lands one feature at a time (spec 052 phase 3 step 2), and every other reader still
uses the mono constants. A reader not in Reads must not run under this entry.

The player fields are differences of CLRFields: name 0x08C and inventory 0x0D4,
less statLife 0x470. TestTheCLRPlayerFieldsAreTheTable checks that.
*/
var clrEntry = Entry{
	Name:     "netfx-4.8.1",
	Family:   NetFx,
	Builds:   []string{"1.4.5.8+24893155"},
	Versions: []string{"4.8.9345.0"},
	Shapes: Shapes{
		ObjectHeader: 0x04,
		StringLenOff: 0x04, StringCharsOff: 0x08,
		ArrLenOff: 0x04, ArrDataOff: 0x08,
		LifeMaxFirst: true,
	},
	Player:      PlayerFields{NameFromLife: -0x3E4, InventoryFromLife: -0x39C},
	LocalPlayer: ByStatics,
	Statics: MainStatics{
		NPCFromPlayer: -0x54, ProjectileFromPlayer: -0x48,
		PlayerLen: 256, NPCLen: 201, ProjectileLen: 1001,
		PlayerActive: 0x70E, LifeInPlayer: 0x470,
	},
	Enabled:    true,
	Reads:      []Feature{ReadPlayer, ReadLocalPlayer},
	Writes:     false,
	Provenance: "cmd/clrfields and cmd/winrecon against the live game, 2026-10-08 (spec 052)",
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
