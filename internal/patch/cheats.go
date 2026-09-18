package patch

import "fmt"

/*
Cheat is a patch applied in place: a few bytes at a known offset inside an
anchor match, swapped for the same number of different bytes.

Everything that fits in the space the game already uses is one of these. When
what is wanted is longer than the site has room for, it becomes an Injection
instead.
*/
type Cheat struct {
	Name     string
	Label    string
	Anchor   string // which pattern finds the code
	PatchOff int    // where in the match to write
	Orig     []byte // what is there, and what disabling puts back
	Patched  []byte // what enabling writes, when it is fixed

	// ValueOff is a player field to set alongside the patch, as an offset from
	// statLife, or zero when the cheat has none.
	ValueOff int
	ValueF32 bool // the field is a float rather than an integer
	OnValue  float64
	OffValue float64

	Note string

	/*
		MakePatched builds the bytes from a value, for a patch that is tunable in
		place -- an immediate inside an instruction, say. Patched is then unused:
		enabling writes what this returns, disabling restores Orig, and the cheat
		reads as on when the site differs from Orig.
	*/
	MakePatched func(int32) []byte
}

// Tunable reports whether the bytes are built from a value rather than fixed.
func (c Cheat) Tunable() bool { return c.MakePatched != nil }

// Cheats is every in-place patch.
var Cheats = map[string]Cheat{
	"mining": {
		Name: "mining", Label: "Global mining speed (pickSpeed)",
		Anchor: "reset_block", PatchOff: 12,
		// fstp [edi+8D8] becomes fstp st(0) and five nops: the per-frame reset
		// of pickSpeed is thrown away instead of being stored.
		Orig:     []byte{0xD9, 0x9F, 0xD8, 0x08, 0x00, 0x00},
		Patched:  []byte{0xDD, 0xD8, 0x90, 0x90, 0x90, 0x90},
		ValueOff: 0x1A0, ValueF32: true, OnValue: 0.2, OffValue: 1.0,
		Note: "Global mining speed. Lower is faster.",
	},
	"reach": {
		Name: "reach", Label: "Placement reach (blockRange)",
		Anchor: "reset_block", PatchOff: 0,
		// The per-frame `blockRange = 0` reset, nopped out.
		Orig:     []byte{0xC7, 0x87, 0xF8, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		Patched:  []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		ValueOff: 0x2C0, OnValue: 20, OffValue: 0,
		Note: "Extended placement reach for every item.",
	},
	"pylons": {
		Name: "pylons", Label: "Multiple pylons per biome",
		Anchor: "pylon_place", PatchOff: 0,
		// The whole one-per-biome check becomes `xor eax,eax; ret`. The method
		// is registered with a bad return of 1, so returning 0 always allows it.
		Orig:    []byte{0x55, 0x8B, 0xEC},
		Patched: []byte{0x31, 0xC0, 0xC3},
		Note: "Place more than one pylon of the same type. Needs one pylon placed " +
			"first, so the game compiles the check.",
	},
	/*
		fast_place forces a low itemTime where the placement timing is read.

		`mov eax,1; cmp edi,eax; cmovl edi,eax` -- the max(edi,1) that keeps the
		item time from going below one -- becomes `mov edi,N` and five nops.
		Lower is faster.
	*/
	"fast_place": {
		Name: "fast_place", Label: "Fast placement (ApplyItemTime)",
		Anchor: "place", PatchOff: 20,
		Orig: []byte{0xB8, 0x01, 0x00, 0x00, 0x00, 0x3B, 0xF8, 0x0F, 0x4C, 0xF8},
		MakePatched: func(n int32) []byte {
			return append(append([]byte{0xBF}, i32(max32(n, 1))...),
				0x90, 0x90, 0x90, 0x90, 0x90)
		},
		Note: "Near-instant block placement.",
	},
	/*
		max_minions rewrites ResetEffects' `maxMinions = 1` to `maxMinions = N`,
		so N slots hold every frame and accessory bonuses still stack on top.

		The patch offset is the four-byte immediate inside that instruction, and
		the value is packed straight into it.
	*/
	"max_minions": {
		Name: "max_minions", Label: "Minion cap (maxMinions)",
		Anchor: "reset_minions", PatchOff: 6,
		Orig:        []byte{0x01, 0x00, 0x00, 0x00},
		MakePatched: func(n int32) []byte { return i32(max32(n, 1)) },
		Note:        "Raises the minion (summon) cap.",
	},
}

/*
ValueSpec is a tunable a cheat carries: what kind of number it is, where it may
go and what it means.

The window builds a control from this, so a cheat without one is a plain switch.
*/
type ValueSpec struct {
	F32     bool
	Default float64
	Lo      float64
	Hi      float64
	Unit    string
	// Presets are named choices. With them the window offers a list of labels
	// instead of a number, because some of these values are not a scale anybody
	// can reason about.
	Presets []Preset
}

// Preset is one named choice.
type Preset struct {
	Label string
	Value float64
}

// ValueSpecs is every tunable, under the name of the cheat it belongs to.
var ValueSpecs = map[string]ValueSpec{
	"mining":       {F32: true, Default: 0.2, Lo: 0.05, Hi: 2.0, Unit: "pickSpeed · lower = faster"},
	"reach":        {Default: 20, Lo: 0, Hi: 100, Unit: "extra tiles"},
	"tool_reach":   {Default: 30, Lo: 1, Hi: 200, Unit: "tiles · mining & interaction"},
	"pickup":       {Default: 50, Lo: 2, Hi: 500, Unit: "× grab range"},
	"spawn_rate":   {Default: 15, Lo: 0, Hi: 200, Unit: "max active enemies · 0 = peaceful"},
	"loot":         {Default: 100, Lo: 1, Hi: 100, Unit: "% min drop chance · 100 = guaranteed"},
	"max_minions":  {Default: 10, Lo: 1, Hi: 255, Unit: "minion slots (base; +accessories)"},
	"smart_cursor": {Default: 20, Lo: 3, Hi: 200, Unit: "tiles"},
	// itemTime presets, lower being faster. "Fast" is the original behaviour.
	"fast_place": {Default: 4, Lo: 1, Hi: 4, Unit: "placement speed",
		Presets: []Preset{{"Fast", 4}, {"Faster", 2}, {"Hyper", 1}}},
	/*
		ore_extract is not a value patched into the game: the extractor's stub is
		built from live state and ignores it.

		It rides the same plumbing so the choice gets a control, is saved with
		the profile and comes back on auto-restore, and the watcher reads it to
		decide whether to sweep gems. Without it, --gems existed on the command
		line and was simply unreachable from the window.
	*/
	"ore_extract": {Default: 0, Lo: 0, Hi: 1, Unit: "what to sweep",
		Presets: []Preset{{"Ores only", 0}, {"Ores + gems", 1}}},
}

/*
Sections are how the patches are grouped in the window, in display order.

A cheat missing from here falls into the last section, so adding one never hides
it.
*/
var Sections = []struct {
	Name  string
	Names []string
}{
	{"Build", []string{"mining", "reach", "fast_place", "tool_reach", "smart_cursor",
		"pylons", "ore_extract"}},
	{"Combat", []string{"max_minions", "spawn_rate", "loot"}},
	{"Accessories", []string{"vanity_accs", "inventory_accs"}},
	{"Misc", []string{"pickup", "teleport", "auto_use"}},
}

// SectionOf is which group a cheat belongs to.
func SectionOf(name string) string {
	for _, s := range Sections {
		for _, n := range s.Names {
			if n == name {
				return s.Name
			}
		}
	}
	return Sections[len(Sections)-1].Name
}

/*
Info is one patch described for a caller that does not care how it works: the
in-place cheats and the injections merged into one catalog.
*/
type Info struct {
	Name    string     `json:"name"`
	Label   string     `json:"label"`
	Note    string     `json:"note"`
	Value   *ValueSpec `json:"value"`
	Kind    string     `json:"kind"` // "cheat" or "injection"
	Section string     `json:"section"`
}

/*
CheatOrder and InjectionOrder are the order the two tables were declared in.

Go's maps have no order, and the catalog's does not come from the section lists:
see Catalog. So the declaration order is written down rather than recovered.
*/
var (
	CheatOrder     = []string{"mining", "reach", "pylons", "fast_place", "max_minions"}
	InjectionOrder = []string{"tool_reach", "pickup", "spawn_rate", "loot", "vanity_accs",
		"smart_cursor", "inventory_accs", "ore_extract", "auto_use", "teleport"}
)

/*
Catalog is every patch, in the order the window shows them.

Grouped by section, and *within* a section by the order the patches were
declared -- in-place cheats first, then injections. The order of names inside a
Sections entry does not affect this and never has: it decides only which group a
patch belongs to. That is worth saying because it reads as though it should,
and rearranging one of those lists to reorder the window would do nothing.
*/
func Catalog() []Info {
	rank := map[string]int{}
	for i, s := range Sections {
		for _, n := range s.Names {
			rank[n] = i
		}
	}
	rankOf := func(name string) int {
		if i, ok := rank[name]; ok {
			return i
		}
		return len(Sections)
	}

	out := make([]Info, 0, len(Cheats)+len(Injections))
	add := func(name, label, note, kind string) {
		var spec *ValueSpec
		if s, ok := ValueSpecs[name]; ok {
			spec = &s
		}
		out = append(out, Info{Name: name, Label: label, Note: note,
			Value: spec, Kind: kind, Section: SectionOf(name)})
	}
	for _, name := range CheatOrder {
		c := Cheats[name]
		add(c.Name, c.Label, c.Note, "cheat")
	}
	for _, name := range InjectionOrder {
		inj := Injections[name]
		add(inj.Name, inj.Label, inj.Note, "injection")
	}

	// Stable, so patches sharing a section keep the order they were added in.
	sortInfos(out, func(a, b Info) bool { return rankOf(a.Name) < rankOf(b.Name) })
	return out
}

/*
declared reports whether the two order lists name every patch there is.

A patch left out of one of them would simply not appear in the window, and one
named twice would appear twice. Neither is a failure anything else would notice.
*/
func declared() error {
	if len(CheatOrder) != len(Cheats) {
		return fmt.Errorf("patch: CheatOrder names %d of %d cheats",
			len(CheatOrder), len(Cheats))
	}
	if len(InjectionOrder) != len(Injections) {
		return fmt.Errorf("patch: InjectionOrder names %d of %d injections",
			len(InjectionOrder), len(Injections))
	}
	for _, name := range CheatOrder {
		if _, ok := Cheats[name]; !ok {
			return fmt.Errorf("patch: CheatOrder names %q, which is not a cheat", name)
		}
	}
	for _, name := range InjectionOrder {
		if _, ok := Injections[name]; !ok {
			return fmt.Errorf("patch: InjectionOrder names %q, which is not an injection", name)
		}
	}
	return nil
}

// The order lists are checked at startup: a patch missing from one of them is a
// patch the window silently stops offering.
func init() {
	if err := declared(); err != nil {
		panic(err.Error())
	}
}
