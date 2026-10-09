package layout

/*
clrEntry is .NET Framework on native Windows, measured in spec 052 phase 0.

Enabled for reading the player, which copy is live and their inventory, and never
for writing yet: the CLR read path
lands one feature at a time (spec 052 phase 3 step 2), and every other reader still
uses the mono constants. A reader not in Reads must not run under this entry.

The player fields are differences of CLRFields: name 0x08C and inventory 0x0D4,
less statLife 0x470. TestTheCLRPlayerFieldsAreTheTable checks that.
*/
var clrEntry = Entry{
	Name:     "netfx-4.8.1",
	Family:   NetFx,
	Builds:   []string{Build1458s24893155},
	Versions: []string{"4.8.9345.0"},
	Shapes: Shapes{
		ObjectHeader: 0x04,
		StringLenOff: 0x04, StringCharsOff: 0x08,
		ArrLenOff: 0x04, ArrDataOff: 0x08,
		LifeMaxFirst: true,
	},
	Player: PlayerFields{NameFromLife: -0x3E4, InventoryFromLife: -0x39C,
		LifeMaxFromLife: -0x08, LifeMax2FromLife: -0x04,
		ManaFromLife: 0x04, ManaMaxFromLife: 0x08, ManaMax2FromLife: 0x0C},
	// Item: CLRFields["Item"], by name (TestTheCLRItemFieldsAreTheTable). No
	// SelectedItemFromLife: the CLR has no selectedItem field, only a
	// selectedItemState struct, and which word of it is the index is unmeasured.
	Item: ItemFields{
		Type: 0x050, Stack: 0x064, UseTime: 0x060, UseAnim: 0x05C,
		Pick: 0x06C, TileBoost: 0x078, Damage: 0x088, Rare: 0x0B8,
		Defense: 0x0A4, BuffType: 0x0DC, Mana: 0x0D4, Crit: 0x0EC,
		Knockback: 0x08C, Scale: 0x09C, ShootSpeed: 0x0C0,
		FishingPole: 0x048, Bait: 0x04C, Prefix: 0x12E,
		AutoReuse: 0x111, Accessory: 0x10E, Favorited: 0x10C,
		Consumable: 0x110, Melee: 0x12F, Magic: 0x130, Ranged: 0x131,
		Summon: 0x132,
		// Past the five reference fields at 0x04..0x17; to the end of the object,
		// BaseSize 0x148 less the sync block (the Item MethodTable on the live
		// game, 2026-10-08). TestTheCLRCopySpanHoldsNoReference.
		CopyLo: 0x018, CopyHi: 0x144,
	},
	LocalPlayer: ByStatics,
	Statics: MainStatics{
		NPCFromPlayer: -0x54, ProjectileFromPlayer: -0x48,
		PlayerLen: 256, NPCLen: 201, ProjectileLen: 1001,
		PlayerActive: 0x70E, LifeInPlayer: 0x470,
	},
	Enabled: true,
	Reads:   []Feature{ReadPlayer, ReadLocalPlayer, ReadInventory},
	WriteFeatures: []Feature{WritePlayerStats, WriteItemFields, WriteItemTemplates,
		CheatFeature("mining"), CheatFeature("reach"),
		CheatFeature("max_minions"), CheatFeature("fast_place"), CheatFeature("pylons"),
		CheatFeature("pickup"), CheatFeature("spawn_rate"), CheatFeature("loot")},
	Writes:       false,
	PlayerValues: map[string]int{"pickSpeed": 0xA4, "blockRange": 0x100},
	Anchors:      netfxAnchors,
	Cheats:       netfxCheats,
	Injections:   "netfx",
	Provenance:   "cmd/clrfields and cmd/winrecon against the live game, 2026-10-08 (spec 052)",
}

/*
netfxAnchors are the patterns the .NET Framework entry's in-place cheats resolve
through, found in the CLR's compiled code on the live game (spec 052 phase 4,
2026-10-09).

reset_block is Player.ResetEffects' run `blockRange = 0; pickSpeed = 1f;` and
three bool stores (false, false, true) -- the same IL sequence as mono's anchor of
that name, compiled differently: the CLR JIT keeps zero in edx and stores it with
a 6-byte `mov [esi+0x570], edx` where mono used a 10-byte `mov [edi+..], 0`, and
`this` is esi, not edi. The two instructions the cheats overwrite are
wildcarded; uniqueness comes from fld1 and the three bool stores. It matched once
on the live game; mono's reset_block matched nothing there.

reset_block's ledger: mining and reach enabled through it on the maintainer's
Windows game, 2026-10-09 -- faster mining and longer placement reach confirmed in
play -- then disabled, and the site read back as its original bytes.
*/
var netfxAnchors = map[string]AnchorDef{
	"reset_block": {
		Pattern: "?? ?? ?? ?? ?? ?? D9 E8 ?? ?? ?? ?? ?? ?? " +
			"88 96 D8 08 00 00 88 96 DE 08 00 00 C6 86 DF 08 00 00 01",
		Verified: []string{Build1458s24893155},
	},
	/*
		Player.ResetEffects: `maxMinions = 1` (maxMinions at 0x30C, CLRFields)
		followed by `maxTurrets = 1` (0x5A8) -- mono's anchor of this name, with esi
		for edi. The immediate is wildcarded so the anchor resolves with the cap
		cheat applied. One match on the live game, 2026-10-09.
	*/
	"reset_minions": {
		Pattern: "C7 86 0C 03 00 00 ?? ?? ?? ?? C7 86 A8 05 00 00 01 00 00 00",
		// 2026-10-09: a cap of 10 confirmed in play on Windows, then disabled and
		// the immediate read back as 1.
		Verified: []string{Build1458s24893155},
	},
	/*
		Player.ApplyItemTime(Item, float), whole: useTime (Item +0x60) to float,
		times the multiplier, truncated; then `if (useTime > 0 && time <= 0) time
		= 1` and the call that stores itemTime and itemTimeMax. The clamp's 15
		bytes are wildcarded: fast_place replaces them, and both its jumps land on
		the `mov edx, eax` after them. One match on the live game, 2026-10-09; a
		second method with the same tail takes the time as an int and does not
		start with the useTime load.
	*/
	"place": {
		Pattern: "8B 52 60 89 55 FC DB 45 FC D9 5D FC D9 45 FC D8 4D 08 DD 5D F4 " +
			"F2 0F 10 45 F4 F2 0F 2C C0 " +
			"?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? 8B D0 E8",
		// 2026-10-09: faster placement confirmed in play on Windows, then disabled
		// and the clamp read back as it was.
		Verified: []string{Build1458s24893155},
	},
	/*
		TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace, whole -- the
		IL mono's anchor of this name describes, compiled with no frame:

			movzx eax, byte [esp+0xC]        ; style, as the pylon type
			mov ecx, [Main.PylonSystem]      ; the static's slot, ASLR'd
			mov edx, eax ; cmp [ecx], ecx ; call HasPylonOfType
			test eax, eax ; jz ; mov eax, 1 ; ret 0x10 ; xor eax, eax ; ret 0x10

		Found through the address of Main.PylonSystem's slot (Main.player's plus
		0x48, CLRFields' Main statics) among the code that loads it. The first five
		bytes are the ones pylons overwrites, so they are wildcarded. Compiled only
		once a pylon's placement preview has been shown; one match on the live game
		then, 2026-10-09.
	*/
	"pylon_place": {
		Pattern: "?? ?? ?? ?? ?? 8B 0D ?? ?? ?? ?? 8B D0 39 09 E8 ?? ?? ?? ?? " +
			"85 C0 74 08 B8 01 00 00 00 C2 10 00 33 C0 C2 10 00",
		// 2026-10-09: a second pylon of a type already placed went down on
		// Windows, then disabled and the movzx read back.
		Verified: []string{Build1458s24893155},
	},
}

// netfxCheats are the .NET Framework entry's in-place cheats.
var netfxCheats = map[string]CheatSite{
	// `fstp [esi+0x514]` (pickSpeed) becomes fstp st(0) and four nops: the reset
	// is popped and discarded, keeping the x87 stack balanced.
	"mining": {Anchor: "reset_block", PatchOff: 8,
		Orig:    []byte{0xD9, 0x9E, 0x14, 0x05, 0x00, 0x00},
		Patched: []byte{0xDD, 0xD8, 0x90, 0x90, 0x90, 0x90}},
	// `mov [esi+0x570], edx` (blockRange = 0), nopped out.
	"reach": {Anchor: "reset_block", PatchOff: 0,
		Orig:    []byte{0x89, 0x96, 0x70, 0x05, 0x00, 0x00},
		Patched: []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90}},
	// The immediate of `maxMinions = 1`.
	"max_minions": {Anchor: "reset_minions", PatchOff: 6,
		Orig:    []byte{0x01, 0x00, 0x00, 0x00},
		Encoder: "imm32-min1"},
	// The clamp `test edx,edx; jle; test eax,eax; jg; mov eax,1; jmp` becomes
	// `mov eax,N` and ten nops: every item time ApplyItemTime sets is N.
	"fast_place": {Anchor: "place", PatchOff: 30,
		Orig: []byte{0x85, 0xD2, 0x7E, 0x0B, 0x85, 0xC0, 0x7F, 0x07,
			0xB8, 0x01, 0x00, 0x00, 0x00, 0xEB, 0x00},
		Encoder: "mov-eax-imm32-min1"},
	// The one-per-biome check's first instruction becomes `xor eax,eax; ret
	// 0x10`: it returns 0, can place, popping its four stack arguments.
	"pylons": {Anchor: "pylon_place", PatchOff: 0,
		Orig:    []byte{0x0F, 0xB6, 0x44, 0x24, 0x0C},
		Patched: []byte{0x33, 0xC0, 0xC2, 0x10, 0x00}},
}
