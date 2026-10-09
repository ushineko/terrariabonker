package layout

/*
Code patches as version-table data.

A cheat that patches the game's compiled code in place is, under one entry, four
facts: a byte pattern that finds the method, where in the match to write, what
is there, and what goes there instead. All four are properties of the code the
entry's runtime compiled, so they live on the entry beside the field offsets --
a different runtime, or a different version of one, is a different entry, not a
branch in the code that applies them.

Plain data only. This package is the leaf every reader imports, so a pattern is
a string the patch package parses, and a value-built patch names its encoder
rather than carrying a function.
*/

// AnchorDef is a byte pattern that finds a method's compiled code: hex bytes
// and "??" wildcards, in the syntax patch.Parse reads.
type AnchorDef struct {
	Pattern string
	// Unique: more than one match is a failure rather than copies to patch.
	Unique bool
	// Verified is a ledger, not a gate: the builds where the cheats using this
	// pattern were confirmed working in play.
	Verified []string
}

/*
CheatSite is an in-place cheat under one entry: the anchor that finds the code,
where in the match to write, the bytes that are there, and the bytes that replace
them -- or, for a cheat whose bytes are built from a value, the encoder that
builds them.
*/
type CheatSite struct {
	Anchor   string // a key of the entry's Anchors
	PatchOff int
	Orig     []byte
	Patched  []byte // nil when Encoder builds the bytes
	Encoder  string // a key of patch's encoder registry; "" for fixed bytes
}

// CheatFeature is the write feature that applying one cheat needs, so an entry
// can allow cheats one at a time as their sites are derived.
func CheatFeature(name string) Feature { return Feature("cheat:" + name) }
