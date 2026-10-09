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

/*
Entry is one supported combination: a runtime family, the builds and runtime
versions its numbers were derived or confirmed on, and whether memory work is
enabled for it.
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
	// Enabled is whether this program reads and writes game memory under the
	// entry. An entry can exist before its read path does.
	Enabled bool
	Shapes  Shapes
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
	Enabled:    true,
	Provenance: "Cheat Engine mono dissector and cmd/monofields, 1.4.5.7 and 1.4.5.8",
}

/*
clrEntry is .NET Framework on native Windows, measured in spec 052 phase 0.

Disabled: its shapes and offsets are measured, and nothing reads memory with them
yet. Enabling it is the end of the CLR read path, not the start.
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
	Enabled:    false,
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
	// anything -- the same rule as an unreadable game version -- so it is let
	// through as today.
	RuntimeUnknown Support = "unknown"
)

/*
Select is the entry for a running build and runtime, and how well it fits.

The entry returned for Candidate is the family's first enabled one, which is
the nearest the table has. For Unsupported and RuntimeUnknown it is the zero
Entry.
*/
func Select(build, runtime string) (Entry, Support) {
	rt, ok := ParseRuntime(runtime)
	if !ok {
		return Entry{}, RuntimeUnknown
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

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
