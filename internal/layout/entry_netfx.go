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
		CheatFeature("mining"), CheatFeature("reach")},
	Writes:       false,
	PlayerValues: map[string]int{"pickSpeed": 0xA4, "blockRange": 0x100},
	Anchors:      netfxAnchors,
	Cheats:       netfxCheats,
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
}
