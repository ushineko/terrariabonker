package patch

import (
	"fmt"

	"github.com/ushineko/terrariabonker/internal/layout"
)

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
tool reach (30), the smart cursor clamp (20), the vanity accessory slots, map-ping teleport, inventory accessories and
the ore extractor were each confirmed in play on the maintainer's Windows game, then disabled and
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
	/*
		Main.TriggerPing(Vector2 position), hooked at offset 3 -- after `push ebp;
		mov ebp,esp` so [ebp+8]/[ebp+0C] are the ping's world X/Y (floats). The
		early-out `cmp byte [abs],0; jz; ...; ret 8` and the Vector2 load make it
		unique; the ASLR'd static address is wildcarded. The six displaced bytes
		(push esi; sub esp,0C; xor eax,eax) are reproduced by the stub.
	*/
	"trigger_ping": {Pattern: MustParse(
		"55 8B EC ?? ?? ?? ?? ?? ?? 89 45 F0 80 3D ?? ?? ?? ?? 00 74 08 " +
			"8D 65 FC 5E 5D C2 08 00 8D 45 08"),
		Verified: netfxVerified},
	/*
		Player.Teleport(Vector2 newPos, int Style, int extraInfo): the call target
		for the ping hook. Anchored on the two constant field stores near its start
		(`mov [ebx+6A4],0x64; mov [ebx+354],4`), position-independent; the method
		entry is the anchor less 0x21.
	*/
	"player_teleport": {Pattern: MustParse(
		"C7 83 A4 06 00 00 64 00 00 00 C7 83 54 03 00 00 04 00 00 00"),
		Verified: netfxVerified},
	/*
		UpdateEquips' first loop, which walks all 58 inventory slots every frame
		(vanilla uses it for info and mechanical accessories -- why a Depth Meter
		works from the bag). Matched from the Player.inventory read (esi+0xD4)
		through the bounds check and the element load, to where the Item is in eax.
		The seven bytes the stub displaces are wildcarded so a cold re-resolve
		matches with the hook in.
	*/
	"inventory_scan": {Pattern: MustParse(
		"8B 86 D4 00 00 00 3B 58 04 0F 83 ?? ?? ?? ?? 8B 44 98 08 " +
			"?? ?? ?? ?? ?? ?? ?? E8"),
		Verified: netfxVerified},
	/*
		Entries of the three methods an equipped accessory goes through, each at
		its prologue (offset 0): ApplyEquipFunctional (the effects),
		GrantPrefixBenefits (modifier benefits) and GrantArmorBenefits (per-item
		extras). The inventory stub calls these for each accessory in the bag.
	*/
	"apply_func": {Pattern: MustParse(
		"55 8B EC 57 56 53 81 EC 08 01 00 00 33 C0 89 85 1C FF FF FF"),
		Verified: netfxVerified},
	"grant_prefix": {Pattern: MustParse(
		"55 8B EC 80 BA 2E 01 00 00 3E 75 06 FF 81 64 04 00 00 80 BA 2E 01 00 00 3F"),
		Verified: netfxVerified},
	"grant_armor": {Pattern: MustParse(
		"55 8B EC 57 56 53 8B F1 8B FA 8B 5F 50 8B CE 8B D3 E8 ?? ?? ?? ?? 8B CE 8B D3 E8"),
		Verified: netfxVerified},
	/*
		Player.GrabItems' entry: a once-per-frame method (Player.Update calls it),
		where the ore drain runs. The five displaced prologue bytes (push ebp; mov
		ebp,esp; push edi; push esi) are wildcarded; the sub esp,0x68 and the
		rep-stosd of 9 make it unique. KillTile does not re-enter GrabItems, so
		draining here is safe.
	*/
	"grabitems_entry": {Pattern: MustParse(
		"?? ?? ?? ?? ?? 53 83 EC 68 8B F1 8D 7D C8 B9 09 00 00 00 33 C0 F3 AB 8B CE 89 55 F0"),
		Verified: netfxVerified},
	/*
		WorldGen.KillTile(int x, int y, bool fail, bool effectOnly, bool noItem),
		at its entry: a static method, x in ecx and y in edx, the three bools on
		the stack. The drain calls it per queued tile to break the vein.
	*/
	"kill_tile": {Pattern: MustParse(
		"55 8B EC 57 56 53 81 EC 90 00 00 00 8B F1 8D 7D 84 B9 19 00 00 00 33 C0 F3 AB 8B CE 89 4D F0 89 55 EC"),
		Verified: netfxVerified},
	"trydrop": {Pattern: MustParse(
		"55 8B EC 57 56 53 ?? ?? ?? ?? ?? 8B 4D ?? 39 09 E8 ?? ?? ?? ?? " +
			"3B 46 18 7D 30 8B 7D 08 8B 5E 08 8B 56 10 8B 46 14"),
		Verified: netfxVerified},
	/*
		Player.ItemCheck's entry: the per-frame item tick, where the auto-use stub
		presses the use button. `this` is in ecx (the CLR instance convention); the
		five displaced prologue bytes (push ebp; mov ebp,esp; push edi; push esi)
		are wildcarded. The frame (sub esp,0x35C) and the rep-stosd of 0xCB dwords
		zeroing it make it unique -- Player.Update, the closest twin by first bytes,
		has a 0xCAC frame and a stosd of 0x320, and GrabItems a 0x68 frame and 9.

		Why here and not Player.Update's entry (where this hook first lived): the
		control byte is set once per frame and read later the same frame, and
		anything written before the read is overwritten by the game's own input
		latching in between. ItemCheck is where the use control is read -- it
		compares controlUseItem at +0x3D9, having touched nothing that clears it
		since entry -- so a write at this entry is the last word before the read.
		A write at Update's entry, ~50 IL earlier, was latched over and never reeled.
	*/
	"item_check": {Pattern: MustParse(
		"?? ?? ?? ?? ?? 53 81 EC 5C 03 00 00 8B F1 8D BD C8 FC FF FF " +
			"B9 CB 00 00 00 33 C0 F3 AB 8B CE"),
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
	/*
		Map-ping teleport: write the ping's position into the live player, no call
		into the game. The player object is baked in at enable (PlacePlayerBody),
		so re-toggle after a world reload -- the same note mono's teleport carries.
	*/
	"teleport": {
		Name: "teleport", Anchor: "trigger_ping", InjectOff: 3,
		Overwrite: []byte{0x56, 0x83, 0xEC, 0x0C, 0x33, 0xC0},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			res := b.Scanner.Resolve("player_teleport", "")
			if !res.Available {
				return nil, fmt.Errorf("%s", res.Reason)
			}
			return TeleportCallBody(b.LivePlayer, res.Sites[0]-0x21, inj.Overwrite)
		},
		RerunOverwrite: false, Arena: true,
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
	/*
		Accessories work from the inventory: the stub runs the three accessory
		methods for each inventory item that is an accessory, the same ones an
		equipped slot goes through. Like mono's, it calls managed code from the
		arena -- per inventory item per frame -- so it carries the GC risk the
		research describes; the objects it touches are rooted in the inventory
		array, so in practice the redundant roots usually cover the gap.
	*/
	"inventory_accs": {
		Name: "inventory_accs", Anchor: "inventory_scan", InjectOff: 19,
		Overwrite: []byte{0x8B, 0x78, 0x50, 0x8B, 0xCE, 0x8B, 0xD7},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			return InvAccsBodyCLR(b, inj.Overwrite)
		},
		RerunOverwrite: false, Arena: true,
	},
	/*
		Ore extractor: a once-per-frame drain at GrabItems' entry that breaks every
		tile the unprivileged side queued, by calling WorldGen.KillTile. Like mono's
		it calls a managed method from the arena, but only while a vein is armed
		(just after the player mines an ore), a handful of frames of KillTile calls
		whose only objects are the permanently-rooted world tiles.
	*/
	"ore_extract": {
		Name: "ore_extract", Anchor: "grabitems_entry", InjectOff: 0,
		Overwrite: []byte{0x55, 0x8B, 0xEC, 0x57, 0x56},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			return OreExtractBodyCLR(b, inj.Overwrite)
		},
		RerunOverwrite: false, Arena: true,
	},
	/*
		Auto-use: a per-frame stub at Player.ItemCheck's entry that presses the use
		button once when the trainer arms it. No call into the game -- it sets two
		control bytes on the player (in ecx) and returns -- so it carries none of
		teleport's GC risk. ItemCheck is the point where the use control is read, so
		the press lands in time to reel (see the item_check anchor). Ships off;
		auto-catch is what arms it.
	*/
	"auto_use": {
		Name: "auto_use", Anchor: "item_check", InjectOff: 0,
		Overwrite: []byte{0x55, 0x8B, 0xEC, 0x57, 0x56},
		BuildBody: func(b *Builder, inj Injection) ([]byte, error) {
			return NetfxAutoUseBody(b.Arena, inj.Overwrite), nil
		},
		RerunOverwrite: false, Arena: true,
	},
	// The denominator load is reproduced with a cap; all four twins.
	"loot": {
		Name: "loot", Anchor: "trydrop", InjectOff: 6,
		Overwrite: []byte{0x8B, 0xF1, 0x8B, 0x56, 0x0C},
		MakeBody:  CapDropDenomEDX, RerunOverwrite: false, Multi: true, Arena: true,
	},
}

/*
TeleportCallBody is the map-ping teleport stub.

It calls Player.Teleport(newPos, Style=0, extraInfo=0) so the warp gets the
game's own sound and dust, matching the Linux build. The CLR passes the instance
in ecx and the int Style in edx; the Vector2 newPos and extraInfo are on the
stack. TriggerPing delivers the ping in *tile* coordinates and Teleport wants
world pixels at sixteen to the tile, so each coordinate is scaled x16 by adding
four to its float exponent (f32Times16). esp is saved in ebx across the call and
restored after, so the stub is right whether the callee cleans the stack or not
(the same guard mono's TeleportBody uses). playerObj and teleport are baked at
enable.

This is the one CLR cheat that calls a managed method from the arena. The thread
is in cooperative GC mode with no transition frame, so a GC during the call can
miss roots in the frames below and crash -- but teleport fires once per ping, a
rare user action, so the window is tiny. (The ore extractor, which would call per
tile continuously, does not take this path.)
*/
func TeleportCallBody(playerObj, teleport uint32, overwrite []byte) ([]byte, error) {
	if playerObj == 0 {
		return nil, fmt.Errorf("no player found -- load into a world and re-enable teleport")
	}
	out := []byte{0x60}                 // pushad
	out = append(out, 0x8B, 0xDC)       // mov ebx,esp -- saved for cleanup-agnostic return
	out = append(out, 0x8B, 0x45, 0x08) // mov eax,[ebp+08] -- ping tile X
	out = append(out, 0x05)             // add eax, x16 -> world pixel
	out = append(out, i32(f32Times16)...)
	out = append(out, 0x8B, 0x4D, 0x0C) // mov ecx,[ebp+0C] -- ping tile Y
	out = append(out, 0x81, 0xC1)       // add ecx, x16
	out = append(out, i32(f32Times16)...)
	// newPos is at [ebp+0C]/[ebp+10] and extraInfo at [ebp+08] in Teleport, so the
	// pushes put Y highest, then X, then extraInfo lowest; Style rides in edx.
	out = append(out, 0x51)       // push ecx -- newPos.Y -> [esp+0C]
	out = append(out, 0x50)       // push eax -- newPos.X -> [esp+08]
	out = append(out, 0x6A, 0x00) // push 0 -- extraInfo  -> [esp+04]
	out = append(out, 0x33, 0xD2) // xor edx,edx -- Style 0
	out = append(out, 0xB9)       // mov ecx, playerObj -- this
	out = append(out, u32(playerObj)...)
	out = append(out, 0xB8) // mov eax, teleport
	out = append(out, u32(teleport)...)
	out = append(out, 0xFF, 0xD0) // call eax
	out = append(out, 0x8B, 0xE3) // mov esp,ebx
	out = append(out, 0x61)       // popad
	return append(out, overwrite...), nil
}

/*
OreExtractBodyCLR drains the ore queue once per frame, calling WorldGen.KillTile
for each queued tile. The queue is a count at the arena's OreQueueOff followed by
(x, y) int pairs; the count is consumed before the work so a batch is mined once,
not every frame. KillTile is static: x in ecx, y in edx, three false bools pushed
(break the tile, really remove it, spawn the drop). esp is saved in ebx and
restored after each call so the stub is right whichever way KillTile cleans the
stack. The five displaced prologue bytes follow.
*/
func OreExtractBodyCLR(b *Builder, overwrite []byte) ([]byte, error) {
	res := b.Scanner.Resolve("kill_tile", "")
	if !res.Available {
		return nil, fmt.Errorf("%s", res.Reason)
	}
	kill := res.Sites[0]
	queue := b.Arena + OreQueueOff

	loop := []byte{0x6A, 0x00, 0x6A, 0x00, 0x6A, 0x00} // push 0 x3: fail, effectOnly, noItem
	loop = append(loop, 0x8B, 0x0E)                    // mov ecx,[esi]   -- x
	loop = append(loop, 0x8B, 0x56, 0x04)              // mov edx,[esi+4] -- y
	loop = append(loop, 0xB8)                          // mov eax, kill
	loop = append(loop, u32(kill)...)
	loop = append(loop, 0xFF, 0xD0)       // call eax
	loop = append(loop, 0x8B, 0xE3)       // mov esp,ebx
	loop = append(loop, 0x83, 0xC6, 0x08) // add esi,8 -- next pair
	loop = append(loop, 0x4F)             // dec edi
	back := 256 - (len(loop) + 2)
	if back < 128 {
		return nil, fmt.Errorf("the extractor loop is %d bytes, too far to jump back", len(loop)+2)
	}
	loop = append(loop, 0x75, byte(back)) //nolint:gosec // checked above

	setup := []byte{0xC7, 0x05} // mov dword [queue],0 -- consume the count
	setup = append(setup, u32(queue)...)
	setup = append(setup, 0x00, 0x00, 0x00, 0x00)
	setup = append(setup, 0x3D) // cmp eax, OreMaxBatch
	setup = append(setup, i32(int32(OreMaxBatch))...)
	setup = append(setup, 0x76, 0x05) // jbe +5
	setup = append(setup, 0xB8)       // mov eax, OreMaxBatch (clamp)
	setup = append(setup, i32(int32(OreMaxBatch))...)
	setup = append(setup, 0xBE) // mov esi, queue+4 -- first pair
	setup = append(setup, u32(queue+4)...)
	setup = append(setup, 0x8B, 0xF8) // mov edi,eax -- the count, the loop counter
	setup = append(setup, 0x8B, 0xDC) // mov ebx,esp

	skip := len(setup) + len(loop)
	if skip >= 128 {
		return nil, fmt.Errorf("the extractor stub is %d bytes, too far to skip", skip)
	}
	out := []byte{0x60, 0xA1} // pushad; mov eax,[queue] -- the count
	out = append(out, u32(queue)...)
	out = append(out, 0x85, 0xC0)       // test eax,eax
	out = append(out, 0x74, byte(skip)) //nolint:gosec // checked above
	out = append(out, setup...)
	out = append(out, loop...)
	out = append(out, 0x61) // popad -- where the skip lands
	return append(out, overwrite...), nil
}

/*
InvAccsBodyCLR calls GrantPrefixBenefits, GrantArmorBenefits and
ApplyEquipFunctional for each inventory item that is an accessory (Item.accessory
+0x10E), the same path an equipped accessory takes. At the hook eax is the Item
and esi the player. The accessory flag gates the calls, because the loop runs
58 items a frame and the methods are large.

GrantPrefixBenefits and GrantArmorBenefits take the player in ecx and the item in
edx; ApplyEquipFunctional takes the player in ecx, the slot in edx (0 here -- the
slot only indexes a ten-bool hide array) and the item on the stack. esp is saved
in ebx and restored after each call so the stub is right whichever way the callee
cleans the stack. The three displaced bytes that follow are reproduced last.
*/
func InvAccsBodyCLR(b *Builder, overwrite []byte) ([]byte, error) {
	entry := func(anchor string) (uint32, error) {
		res := b.Scanner.Resolve(anchor, "")
		if !res.Available {
			return 0, fmt.Errorf("%s", res.Reason)
		}
		return res.Sites[0], nil
	}
	prefixFn, err := entry("grant_prefix")
	if err != nil {
		return nil, err
	}
	armorFn, err := entry("grant_armor")
	if err != nil {
		return nil, err
	}
	applyFn, err := entry("apply_func")
	if err != nil {
		return nil, err
	}

	call := func(target uint32) []byte {
		out := append([]byte{0xB8}, u32(target)...) // mov eax, target
		return append(out, 0xFF, 0xD0, 0x8B, 0xE3)  // call eax; mov esp,ebx
	}

	guarded := []byte{0x8B, 0xCE, 0x8B, 0xD7} // mov ecx,esi; mov edx,edi (player, item)
	guarded = append(guarded, call(prefixFn)...)
	guarded = append(guarded, 0x8B, 0xCE, 0x8B, 0xD7)
	guarded = append(guarded, call(armorFn)...)
	guarded = append(guarded, 0x57, 0x8B, 0xCE, 0x33, 0xD2) // push edi; mov ecx,esi; xor edx,edx
	guarded = append(guarded, call(applyFn)...)
	if len(guarded) > 127 {
		return nil, fmt.Errorf("the inventory-accessory stub is %d bytes, too far to skip", len(guarded))
	}

	out := []byte{0x60, 0x8B, 0xDC, 0x8B, 0xF8}                 // pushad; mov ebx,esp; mov edi,eax (Item)
	out = append(out, 0x80, 0xBF, 0x0E, 0x01, 0x00, 0x00, 0x00) // cmp byte [edi+10E],0 -- accessory?
	out = append(out, 0x74, byte(len(guarded)))                 //nolint:gosec // bounded above
	out = append(out, guarded...)
	out = append(out, 0x61) // popad
	return append(out, overwrite...), nil
}

/*
CLR Player control offsets for auto-use, object-relative (CLRFields, 2026-10-08):
controlUseItem at +0x7E4 and releaseUseItem at +0x7F1. Setting the control alone
reels a bobber in; starting a use (a fresh cast) needs the release flag too, the
same pair mono's auto-use sets.
*/
const (
	netfxControlUseItemOff = 0x7E4
	netfxReleaseUseItemOff = 0x7F1
)

/*
NetfxAutoUseBody presses the use button once, on the next frame, when the trainer
arms it -- the CLR's auto-use stub.

It hooks Player.Update's entry, where `this` is in ecx (the CLR instance
convention) and the frame is not set up yet, so the player is read straight from
ecx rather than from [ebp+8] as mono's stub does mid-method. pushad/pushfd leave
ecx untouched, so it is still the player when the press runs.

**Consume before acting**, the same ordering the extractor and mono's auto-use
use: the armed flag is cleared before the two field writes, so a stub that dies
between them presses nothing rather than forever. Both control bytes are set --
controlUseItem to reel, releaseUseItem so a cast reads as a fresh press rather
than a hold -- baked from the measured offsets (no arena indirection; the CLR
knows both). The counter is the trainer's own tally, for the arm/read test.

The arena words it reads (armed, count) are the shared arena layout mono uses, so
the AutoUse view drives it unchanged. The displaced entry bytes follow.
*/
func NetfxAutoUseBody(arena uint32, overwrite []byte) []byte {
	armed := arena + AutoUseArmedOff
	count := arena + AutoUseCountOff

	tail := []byte{0xC6, 0x81} // mov byte [ecx+controlUseItem], 1
	tail = append(tail, u32(netfxControlUseItemOff)...)
	tail = append(tail, 0x01)
	tail = append(tail, 0xC6, 0x81) // mov byte [ecx+releaseUseItem], 1
	tail = append(tail, u32(netfxReleaseUseItemOff)...)
	tail = append(tail, 0x01)
	tail = append(tail, 0xFF, 0x05) // inc dword [count]
	tail = append(tail, u32(count)...)

	press := []byte{0x83, 0x25} // and dword [armed], 0 -- consume the flag first
	press = append(press, u32(armed)...)
	press = append(press, 0x00)
	press = append(press, 0x85, 0xC9)            // test ecx, ecx -- the player
	press = append(press, 0x74, byte(len(tail))) //nolint:gosec // a fixed, short body
	press = append(press, tail...)

	out := []byte{0x60, 0x9C} // pushad; pushfd
	out = append(out, 0x83, 0x3D)
	out = append(out, u32(armed)...)
	out = append(out, 0x00)                   // cmp dword [armed], 0
	out = append(out, 0x74, byte(len(press))) //nolint:gosec // a fixed, short body
	out = append(out, press...)
	out = append(out, 0x9D, 0x61) // popfd; popad -- where both skips land
	return append(out, overwrite...)
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
