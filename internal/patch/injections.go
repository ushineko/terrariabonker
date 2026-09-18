package patch

import (
	"sort"

	"github.com/ushineko/terrariabonker/internal/locate"
)

/*
Injection is a cheat that does not fit where it goes.

Some changes are longer than the instructions they replace, so they cannot be
made in place. Instead a few bytes at an injection point are overwritten with a
jump to a stub, the stub runs, and it jumps back. The stub is MakeBody(value),
then the bytes that were displaced, then the jump home.
*/
type Injection struct {
	Name      string
	Label     string
	Anchor    string // which pattern finds the method
	InjectOff int    // where in the match to hook, which may be before it
	Overwrite []byte // the bytes displaced, and what disabling puts back
	Note      string

	// MakeBody builds the injected instructions from a value. Nil for an
	// injection whose stub is built from live state instead.
	MakeBody func(int32) []byte

	/*
		RerunOverwrite says the stub re-runs the displaced bytes after its own:
		force a value, then continue what was interrupted.

		False when the body fully replaces those instructions because they are
		the very computation being overridden -- the drop-chance cheat rewrites
		the denominator load in place -- in which case the body reproduces
		whatever of them still has to happen.
	*/
	RerunOverwrite bool

	/*
		Multi says the anchor is expected to match several sites and the stub is
		installed at every one.

		Those are structural twins whose bodies are identical bar their call
		targets, like CommonDrop and its luck-scaling sibling. Patching one and
		not the other gives a cheat that works for some drops.
	*/
	Multi bool

	/*
		CallAnchor names the anchor for a managed method this stub *calls*, and
		CallTargetOff how far the method's entry sits before that anchor.

		A stub that calls back into the game's own code is a different animal:
		single-site, no tunable value, and it reproduces its displaced bytes
		itself.
	*/
	CallAnchor    string
	CallTargetOff int

	// Edits are byte changes applied and reverted alongside this injection, for
	// a cheat that is a stub plus a couple of in-place changes elsewhere.
	Edits []Edit

	/*
		BuildBody builds the stub from live state -- resolved call targets and
		the like -- rather than from a value. Such a stub reproduces its own
		displaced bytes, so RerunOverwrite does not apply to it.

	*/
	BuildBody func(*Builder, Injection) ([]byte, error)

	/*
		WritesCave says the stub's own code writes somewhere inside its cave.

		A cave is borrowed padding inside somebody else's mapping and those are
		typically read-execute, so such a stub faults the first time it runs --
		while installing it works fine, because /proc/pid/mem bypasses page
		protection. Saying so makes the cave search demand a writable page
		instead of letting the mismatch surface as an access violation mid-game.
	*/
	WritesCave bool

	/*
		Arena says the stub belongs in memory this program allocated rather than
		in borrowed padding: it is too big for a gap, needs to write, or carries
		a buffer. The five-byte site jump still reaches it, since a rel32 spans
		two gigabytes either way.

		Every shipped injection sets this, so WritesCave currently has no effect
		on any of them: it is read only on the cave path. Both are kept because
		the cave path is a deliberate fallback.
	*/
	Arena bool
}

/*
Edit is one byte change inside an anchor match, applied under some cheat's
toggle.

It lets a single switch change more than one place: making the vanity accessory
slots work needs two loop bounds widened, in different parts of UpdateEquips, and
they have to go on and off together or the cheat is half-applied.
*/
type Edit struct {
	Anchor  string
	Off     int
	Orig    []byte
	Patched []byte
}

/*
Builder is what a stub built from live state is given: somewhere to resolve
anchors, something to read, and the arena its data lives in.

The memory is the locator's kind because two of these stubs need the statics that
lead to the live player, which is the locator's business and not this package's.
*/
type Builder struct {
	Scanner *Scanner
	Mem     BuilderMem
	Arena   uint32
}

// BuilderMem is memory a stub can be built against: readable, writable, and
// listable the several ways the pieces of this need.
type BuilderMem interface {
	ArenaMem
	locate.ExecMem
}

/*
ClampVanitySlot maps a vanity accessory slot onto its functional mirror before
the call.

ApplyEquipFunctional uses its slot argument for exactly one thing,
hideVisibleAccessory[slot], and that array holds ten booleans -- so passing 13 to
19 straight through throws every frame. The slot is already in eax at the
injection point, so this needs nothing from the stack frame.

	cmp eax,0xA     a vanity slot?
	jl  +3
	sub eax,0xA     13..19 becomes 3..9, the mirror whose hide-visual flag it follows
*/
func ClampVanitySlot(int32) []byte {
	return []byte{0x83, 0xF8, 0x0A, 0x7C, 0x03, 0x83, 0xE8, 0x0A}
}

/*
ShrinkSmartCursor shrinks the smart cursor's search box to the span between the
player and the cursor, plus n tiles.

SmartCursorLookup sizes that box from GetTileRegion, which calls the GetRanges
that tool_reach forces and then adds blockRange -- so both reach cheats inflate
it, and it is an *area*: 121 tiles a frame at vanilla reach, 22,801 at 75, around
90,000 with both stacked. That is the placement stutter.

Two earlier shapes were wrong, both found in play. Centred on the player, the box
stopped containing the cursor once it moved past n -- and the same four fields are
also the "is the target in reach" test just after this point, so smart placement
dropped out entirely. Centred on the cursor, the span back toward the player was
cut out, and the search works outward from the player, so it failed again as soon
as the two were more than n apart.

So both ends are covered: the original box's midpoint is the player, since
GetTileRegion built it around them; the cursor comes from screenTargetX and Y;
and the result is min-n to max+n of the pair, intersected with the original box
so it can only ever shrink. The area becomes the on-screen separation plus a
margin instead of the reach squared.

The clamp has to be here rather than in GetTileRegion, which has nine callers
including the ones tool_reach exists to extend. esi holds the
SmartCursorUsageInfo, and eax and ecx are dead because the following code reloads
both. The displaced `test ebx,ebx` is reproduced *last*, so the conditional jump
after the jump home sees the flags it expects.
*/
func ShrinkSmartCursor(n int32) []byte {
	n = max32(n, 1)

	// axis is one dimension of the box: the cursor field, and the pair of box
	// fields it is clamped against.
	axis := func(target, start, end byte) []byte {
		out := []byte{0x8B, 0x46, start} // mov eax,[esi+start]
		out = append(out, 0x03, 0x46, end)
		out = append(out, 0xD1, 0xF8) // sar eax,1 -- the player tile
		out = append(out, 0x8B, 0x4E, target)
		out = append(out, 0x3B, 0xC1) // cmp eax,ecx
		out = append(out, 0x7E, 0x01) // jle +1
		out = append(out, 0x91)       // xchg eax,ecx -- eax is the lower
		out = append(out, 0x2D)       // sub eax,n
		out = append(out, i32(n)...)
		out = append(out, 0x81, 0xC1) // add ecx,n
		out = append(out, i32(n)...)
		out = append(out, 0x3B, 0x46, start)
		out = append(out, 0x7E, 0x03) // keep the start when it is already tighter
		out = append(out, 0x89, 0x46, start)
		out = append(out, 0x3B, 0x4E, end)
		out = append(out, 0x7D, 0x03) // keep the end when it is already tighter
		return append(out, 0x89, 0x4E, end)
	}

	out := []byte{0x89, 0x46, 0x3C} // the displaced store, reproduced first
	out = append(out, axis(0x28, 0x30, 0x34)...)
	out = append(out, axis(0x2C, 0x38, 0x3C)...)
	return append(out, 0x85, 0xDB) // test ebx,ebx -- displaced, last, for the flags
}

// Injections is every cheat that needs a stub.
var Injections = map[string]Injection{
	/*
		Injected just before GetRanges' epilogue, where esi and edi still hold
		the two output pointers. The displaced `lea esp,[ebp-0C]; pop esi; pop
		edi` is re-run after the stub forces both outputs past the game's clamp.
	*/
	"tool_reach": {
		Name: "tool_reach", Label: "Tool + interaction reach (GetRanges)",
		Anchor: "getranges", InjectOff: 0xCA,
		Overwrite: []byte{0x8D, 0x65, 0xF4, 0x5E, 0x5F},
		MakeBody:  ForceXY, RerunOverwrite: true, Arena: true,
		Note: "Extends mining, tool use, chests, signs and crafting stations together.",
	},
	/*
		Player.GrabItems: a call returns the grab range in eax, and the stub
		scales it before the store that follows.
	*/
	"pickup": {
		Name: "pickup", Label: "Item pickup range (GrabItems)",
		Anchor: "grabitems", InjectOff: 0,
		Overwrite: []byte{0x89, 0x45, 0xAC, 0x8D, 0x45, 0xB0},
		MakeBody:  ImulEAX, RerunOverwrite: true, Arena: true,
		Note: "Scales the item pickup radius.",
	},
	"spawn_rate": {
		Name: "spawn_rate", Label: "Spawn rate (GetSpawnRate)",
		Anchor: "get_spawn_rate", InjectOff: 0x1EAA,
		Overwrite: []byte{0x8D, 0x65, 0xF4, 0x5E, 0x5F},
		MakeBody:  ForceSpawn, RerunOverwrite: true, Arena: true,
		Note: "Caps active enemies. 0 is peaceful.",
	},
	/*
		The injection point is *before* the anchor, which is what the negative
		offset means: the pattern sits entirely downstream of the bytes the jump
		overwrites, so the anchor is still scannable once the patch is in place
		and disabling never hits a self-corrupted seed.
	*/
	"loot": {
		Name: "loot", Label: "Drop chance floor (CommonDrop)",
		Anchor: "trydrop", InjectOff: -7,
		Overwrite: []byte{0x8B, 0x4E, 0x10, 0x89, 0x4C, 0x24, 0x04},
		MakeBody:  CapDropDenom, RerunOverwrite: false, Multi: true, Arena: true,
		Note: "Minimum drop chance for common drops. 100 is guaranteed.",
	},
	/*
		The stub clamps the slot, and the two edits widen the loops that decide
		which slots are walked at all. They go on and off together, or the cheat
		is half-applied.
	*/
	"vanity_accs": {
		Name: "vanity_accs", Label: "Vanity accessories work (slots 13-19)",
		Anchor: "equip_apply", InjectOff: 7,
		Overwrite: []byte{0x89, 0x44, 0x24, 0x04, 0x89, 0x3C, 0x24},
		MakeBody:  ClampVanitySlot, RerunOverwrite: true, Arena: true,
		Edits: []Edit{
			{Anchor: "equip_apply", Off: 27, Orig: []byte{0x0A}, Patched: []byte{0x14}},
			{Anchor: "equip_benefits", Off: 48, Orig: []byte{0x0A}, Patched: []byte{0x14}},
		},
		Note: "Vanity accessory slots grant full effects, doubling your usable accessories.",
	},
	"smart_cursor": {
		Name: "smart_cursor", Label: "Smart cursor search radius",
		Anchor: "smart_cursor", InjectOff: 69,
		Overwrite: []byte{0x89, 0x46, 0x3C, 0x85, 0xDB},
		MakeBody:  ShrinkSmartCursor, RerunOverwrite: false, Arena: true,
		Note: "Don't set this too high or the game will lag.",
	},
	"inventory_accs": {
		Name: "inventory_accs", Label: "Accessories work from inventory",
		Anchor: "inventory_scan", InjectOff: 22,
		Overwrite: []byte{0x8B, 0x00, 0x8B, 0x40, 0x6C},
		BuildBody: func(b *Builder, _ Injection) ([]byte, error) {
			return InventoryAccsBody(b)
		},
		RerunOverwrite: false, Arena: true,
		Note: "Accessories work from your inventory, without being equipped.",
	},
	"ore_extract": {
		Name: "ore_extract", Label: "Ore extractor (vein mining)",
		Anchor: "grabitems_call", InjectOff: 21,
		Overwrite: []byte{0x89, 0x04, 0x24, 0x8B, 0xC0},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			return OreExtractBody(b, inj.Overwrite)
		},
		RerunOverwrite: false, Arena: true,
		Note: "Mines the rest of an ore vein while you mine it. Whitelisted ores only.",
	},
	"auto_use": {
		Name: "auto_use", Label: "Auto-use (press the use button)",
		Anchor: "borders_movement", InjectOff: 5,
		Overwrite: []byte{0x8B, 0x45, 0x08, 0xC7, 0x80, 0xFC, 0x03, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			return AutoUseBody(b.Arena, inj.Overwrite), nil
		},
		RerunOverwrite: false, WritesCave: true, Arena: true,
		Note: "Lets a cheat press your use button. Ships off; nothing presses it on its own.",
	},
	"teleport": {
		Name: "teleport", Label: "Map-ping teleport (TriggerPing)",
		Anchor: "trigger_ping", InjectOff: 0,
		Overwrite:      []byte{0x8B, 0x4D, 0x08, 0x89, 0x4C, 0x24, 0x04},
		RerunOverwrite: false, Arena: true,
		CallAnchor: "player_teleport", CallTargetOff: 0x32,
		Note: "Double-click the fullscreen map to warp there. Re-toggle after a world reload.",
	},
}

// sortInfos orders a catalog in place, keeping equal entries as they were.
func sortInfos(out []Info, less func(a, b Info) bool) {
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
}
