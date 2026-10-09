package patch

import "github.com/ushineko/terrariabonker/internal/layout"

/*
The .NET Framework injection set: the stub-based cheats as the CLR's JIT compiled
the methods they hook (spec 052).

The same cheats as the mono set, in different code: the CLR passes `this` and the
first argument in ecx and edx, keeps different values in different registers, and
lays fields out at different offsets, so every hook site, displaced run of bytes
and most bodies are its own. Each was found read-only on the live Windows game
(1.4.5.8, netfx 4.8.9345.0, 2026-10-09) and checked for a jump into the bytes it
displaces; none lands anywhere but the first.

The arena is allocated directly ("allocate"): natively on Windows this program
can map memory in the game, so no springboard and no frames are needed.

Labels and notes are the catalog's (the mono set's table); a set only says how
each cheat is applied.
*/

/*
netfxInjectionAnchors are the patterns the CLR set's stubs resolve through.

Ledger: on 2026-10-09 pickup (x10), the drop floor (100%) and the spawn cap (30)
were each confirmed in play on the maintainer's Windows game, then disabled and
every site read back as its original bytes.
*/
var netfxInjectionAnchors = map[string]Anchor{
	/*
		Player.GetItemGrabRange(Item), at its exit: the magnets and rings have
		summed the range into edi, and `mov eax, edi; pop ebx; pop esi; pop edi`
		returns it. Those five bytes are the hook, wildcarded; two branches jump
		to their first byte, none into them. The pattern runs from the last
		bonus's call through the `add edi, 0xF0` it guards.
	*/
	"get_item_grab_range": {Pattern: MustParse(
		"FF 15 ?? ?? ?? ?? 85 C0 74 06 81 C7 F0 00 00 00 ?? ?? ?? ?? ?? 5D C3"),
		Verified: netfxVerified},
	/*
		Spawner.GetSpawnRate, at its exit: esi and edi still point at the out
		spawnRate and maxSpawns -- the shape of mono's -- and `lea esp,[ebp-0C];
		pop ebx; pop esi` begins the epilogue. Those five bytes are the hook,
		wildcarded; the two jumps that reach the exit land on their first byte.
		The pattern is the last modifier's pair of scaled stores before it.
	*/
	"get_spawn_rate": {Pattern: MustParse(
		"85 C0 75 38 DB 06 D9 5D D8 D9 45 D8 D8 0D ?? ?? ?? ?? DD 5D D0 " +
			"F2 0F 10 45 D0 F2 0F 2C C0 89 06 DB 07 D9 5D D8 D9 45 D8 D8 0D ?? ?? ?? ?? " +
			"DD 5D D0 F2 0F 10 45 D0 F2 0F 2C C0 89 07 ?? ?? ?? ?? ?? 5F 5D C2 08 00"),
		Verified: netfxVerified},
	/*
		CommonDrop.TryDroppingItem and its three twins, from the prologue:
		`mov esi, ecx` (the rule) and `mov edx, [esi+0C]` (chanceDenominator, the
		roll's bound) are the hook, wildcarded -- the same five bytes in all four.
		Then the roll, `cmp eax, [esi+18]` (chanceNumerator) and the drop. The
		twins differ in the call and in which argument they pass it, so those are
		wildcarded too.
	*/
	"trydrop": {Pattern: MustParse(
		"55 8B EC 57 56 53 ?? ?? ?? ?? ?? 8B 4D ?? 39 09 E8 ?? ?? ?? ?? " +
			"3B 46 18 7D 30 8B 7D 08 8B 5E 08 8B 56 10 8B 46 14"),
		Verified: netfxVerified},
}

// netfxVerified is the builds the CLR set's confirmed stubs were confirmed on.
var netfxVerified = []string{layout.Build1458s24893155}

// netfxInjections are the CLR set's stub-based cheats.
var netfxInjections = map[string]Injection{
	// The range is scaled in edi before it is moved to eax and returned.
	"pickup": {
		Name: "pickup", Anchor: "get_item_grab_range", InjectOff: 16,
		Overwrite: []byte{0x8B, 0xC7, 0x5B, 0x5E, 0x5F},
		MakeBody:  ImulEDI, RerunOverwrite: true, Arena: true,
	},
	// mono's body: both outputs forced through esi and edi, then the epilogue.
	"spawn_rate": {
		Name: "spawn_rate", Anchor: "get_spawn_rate", InjectOff: 60,
		Overwrite: []byte{0x8D, 0x65, 0xF4, 0x5B, 0x5E},
		MakeBody:  ForceSpawn, RerunOverwrite: true, Arena: true,
	},
	// The denominator load is reproduced with a cap; all four twins.
	"loot": {
		Name: "loot", Anchor: "trydrop", InjectOff: 6,
		Overwrite: []byte{0x8B, 0xF1, 0x8B, 0x56, 0x0C},
		MakeBody:  CapDropDenomEDX, RerunOverwrite: false, Multi: true, Arena: true,
	},
}

/*
ImulEDI is `imul edi, edi, N`, scaling whatever is in edi -- the short encoding
when N fits a byte.
*/
func ImulEDI(n int32) []byte {
	if n >= -128 && n <= 127 {
		return []byte{0x6B, 0xFF, byte(n)} //nolint:gosec // range-checked above
	}
	return append([]byte{0x69, 0xFF}, i32(n)...)
}

/*
CapDropDenomEDX is CapDropDenom for the CLR's TryDroppingItem, where the
denominator goes to the roll in edx. It reproduces both displaced instructions,
the second with the cap:

	mov esi, ecx         the rule
	mov edx, [esi+0C]    chanceDenominator
	cmp edx, cap
	jle +5               already at or below the cap, keep it
	mov edx, cap
*/
func CapDropDenomEDX(pct int32) []byte {
	floor := max32(int32(100)/max32(pct, 1), 1)
	out := []byte{0x8B, 0xF1, 0x8B, 0x56, 0x0C}
	out = append(out, 0x81, 0xFA)
	out = append(out, i32(floor)...)
	out = append(out, 0x7E, 0x05)
	out = append(out, 0xBA)
	return append(out, i32(floor)...)
}
