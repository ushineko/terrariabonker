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

Ledger: on 2026-10-09 pickup (x10), the drop floor (100%), the spawn cap (30),
tool reach (30), the smart cursor clamp (20) and the vanity accessory slots
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
		TileReachCheckSettings.GetRanges(out x, out y), from its entry: the
		tileRangeX and tileRangeY statics times the multiplier into both outputs.
		The hook is its exit, 0xF1 in: `lea esp,[ebp-0C]; pop ebx; pop esi`, where
		edi still holds &x and [ebp+8] is &y. One jump reaches the exit, at its
		first byte.
	*/
	"getranges": {Pattern: MustParse(
		"55 8B EC 57 56 53 83 EC 08 8B F1 8B FA A1 ?? ?? ?? ?? 0F AF 06 89 07 " +
			"A1 ?? ?? ?? ?? 0F AF 06 8B 55 08 89 02"),
		Verified: netfxVerified},
	/*
		SmartCursorHelper.SmartCursorLookup, just after the box GetTileRegion
		filled has been clamped to the world: `mov [ebx+20],eax` (endY) and `cmp
		dword [ebp-3C],0`, the hook, wildcarded; then the in-reach test of the
		cursor against the four edges. The usage info is in ebx. No jump lands
		inside the hook but at its first byte.
	*/
	"smart_cursor": {Pattern: MustParse(
		"?? ?? ?? ?? ?? ?? ?? 74 28 8B 43 0C 3B 43 14 7C 20 8B 43 0C 3B 43 18 7F 18 " +
			"8B 43 10 3B 43 1C 7C 10 8B 43 10 3B 43 20 7F 08"),
		Verified: netfxVerified},
	/*
		Player.UpdateEquips' effects loop, from `mov ebx, 3`:

			for (k = 3; k < 10; k++)
			    if (IsItemSlotUnlockedAndUsable(k))
			        ApplyEquipFunctional(k, GetEffectiveArmor(k))

		The hook is `push eax; mov edx, ebx; mov ecx, esi` before the call, at
		42: the item pushed, the slot into edx. The loop bound is at 56. Both
		are wildcarded; the vanity loop after it starts at 13 and does not
		match. No jump lands inside the hook.
	*/
	"equip_apply": {Pattern: MustParse(
		"BB 03 00 00 00 8B CE 8B D3 E8 ?? ?? ?? ?? 85 C0 74 23 8D 7D ?? 0F 57 C0 " +
			"66 0F D6 07 8D 45 ?? 50 8B CE 8B D3 FF 15 ?? ?? ?? ?? ?? ?? ?? ?? ?? " +
			"FF 15 ?? ?? ?? ?? 43 83 FB ?? 7C CA"),
		Verified: netfxVerified},
	/*
		UpdateEquips' benefit loop: `if (item.accessory) GrantPrefixBenefits(item);
		GrantArmorBenefits(item)` (accessory is Item +0x10E), through the `k < 10`
		bound at 32, wildcarded.
	*/
	"equip_benefits": {Pattern: MustParse(
		"80 BF 0E 01 00 00 00 74 0A 8B CE 8B D7 FF 15 ?? ?? ?? ?? 8B CE 8B D7 " +
			"FF 15 ?? ?? ?? ?? 43 83 FB ?? 0F 8C"),
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
	// Both outputs forced through edi and [ebp+8], then the epilogue.
	"tool_reach": {
		Name: "tool_reach", Anchor: "getranges", InjectOff: 0xF1,
		Overwrite: []byte{0x8D, 0x65, 0xF4, 0x5B, 0x5E},
		MakeBody:  ForceXYOutOnStack, RerunOverwrite: true, Arena: true,
	},
	// mono's clamp, on the CLR's register and offsets.
	"smart_cursor": {
		Name: "smart_cursor", Anchor: "smart_cursor", InjectOff: 0,
		Overwrite: []byte{0x89, 0x43, 0x20, 0x83, 0x7D, 0xC4, 0x00},
		MakeBody:  netfxSmartCursor.shrink, RerunOverwrite: false, Arena: true,
	},
	// The slot clamped in edx on its way to ApplyEquipFunctional, and both loops
	// widened to the vanity slots, together.
	"vanity_accs": {
		Name: "vanity_accs", Anchor: "equip_apply", InjectOff: 42,
		Overwrite: []byte{0x50, 0x8B, 0xD3, 0x8B, 0xCE},
		MakeBody:  ClampVanitySlotEDX, RerunOverwrite: false, Arena: true,
		Edits: []Edit{
			{Anchor: "equip_apply", Off: 56, Orig: []byte{0x0A}, Patched: []byte{0x14}},
			{Anchor: "equip_benefits", Off: 32, Orig: []byte{0x0A}, Patched: []byte{0x14}},
		},
	},
	// The denominator load is reproduced with a cap; all four twins.
	"loot": {
		Name: "loot", Anchor: "trydrop", InjectOff: 6,
		Overwrite: []byte{0x8B, 0xF1, 0x8B, 0x56, 0x0C},
		MakeBody:  CapDropDenomEDX, RerunOverwrite: false, Multi: true, Arena: true,
	},
}

/*
ForceXYOutOnStack is `mov dword [edi],N; mov eax,[ebp+8]; mov dword [eax],N`:
GetRanges' two outputs as the CLR leaves them at its exit, x through edi and y
through its stack argument. eax is free there; the method returns nothing.
*/
func ForceXYOutOnStack(n int32) []byte {
	out := append([]byte{0xC7, 0x07}, i32(n)...)
	out = append(out, 0x8B, 0x45, 0x08)
	return append(append(out, 0xC7, 0x00), i32(n)...)
}

/*
netfxSmartCursor is the CLR's box: the usage info in ebx, the cursor at
0x0C/0x10 and the box at 0x14..0x20 (CLRFields of SmartCursorUsageInfo); the
displaced endY store first, and the `cmp [ebp-3C],0` whose flags the jz after the
hook reads, last.
*/
var netfxSmartCursor = smartCursorBox{
	reg: 3, targetX: 0x0C, targetY: 0x10, startX: 0x14, endX: 0x18, startY: 0x1C, endY: 0x20,
	first: []byte{0x89, 0x43, 0x20},
	last:  []byte{0x83, 0x7D, 0xC4, 0x00},
}

/*
ClampVanitySlotEDX is ClampVanitySlot for the CLR, where the slot reaches
ApplyEquipFunctional in edx: the displaced `push eax; mov edx, ebx` and `mov
ecx, esi` reproduced around the clamp.

	push eax        the item, the call's stack argument
	mov edx, ebx    the slot
	cmp edx, 0xA    a vanity slot?
	jl  +3
	sub edx, 0xA    13..19 becomes 3..9, the mirror whose hide-visual flag it follows
	mov ecx, esi    the player
*/
func ClampVanitySlotEDX(int32) []byte {
	return []byte{0x50, 0x8B, 0xD3, 0x83, 0xFA, 0x0A, 0x7C, 0x03, 0x83, 0xEA, 0x0A, 0x8B, 0xCE}
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
