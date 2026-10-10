package layout

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
	Builds: []string{Build1457s24825745, Build1458s24893155},
	Confirmed: []string{
		"11.3.0: 1.4.5.8+24893155 in a world, 2026-10-08 -- the player located by name, " +
			"stats and inventory read, all 15 cheat anchors resolved, and the maintainer " +
			"confirmed the cheats still work in play",
	},
	Shapes: Shapes{
		ObjectHeader: 0x08,
		StringLenOff: 0x08, StringCharsOff: 0x0C,
		ArrLenOff: ArrLenOff, ArrDataOff: ArrDataOff,
		LifeMaxFirst: true,
	},
	Player: PlayerFields{NameFromLife: NamePtrOff, InventoryFromLife: InventoryPtrOff, SelectedItemFromLife: SelectedItemOff,
		LifeMaxFromLife: StatLifeMaxOff, LifeMax2FromLife: StatLifeMax2Off,
		ManaFromLife: StatManaOff, ManaMaxFromLife: StatManaMaxOff, ManaMax2FromLife: StatManaMax2Off,
		PositionFromLife: 0x0C - 0x738},
	Item: ItemFields{
		Type: ItemType, Stack: ItemStack, UseTime: ItemUseTime, UseAnim: ItemUseAnim,
		Pick: ItemPick, TileBoost: ItemTileBoost, Damage: ItemDamage, Rare: ItemRare,
		Defense: ItemDefense, BuffType: ItemBuffType, Mana: ItemMana, Crit: ItemCrit,
		Knockback: ItemKnockback, Scale: ItemScale, ShootSpeed: ItemShootSpeed,
		FishingPole: ItemFishingPole, Bait: ItemBait, Prefix: ItemPrefix,
		AutoReuse: ItemAutoReuse, Accessory: ItemAccessory, Favorited: ItemFavorited,
		Consumable: ItemConsumable, Melee: ItemMelee, Magic: ItemMagic, Ranged: ItemRanged,
		Summon: ItemSummon,
		CopyLo: CopyLo, CopyHi: CopyHi,
	},
	Reads:        []Feature{ReadPlayer, ReadInventory, ReadLocalPlayer, ReadTiles, ReadProjectiles},
	LocalPlayer:  ByAnchor,
	Writes:       true,
	Enabled:      true,
	PlayerValues: map[string]int{"pickSpeed": PickSpeedOff, "blockRange": BlockRangeOff},
	Anchors:      monoAnchors,
	Cheats:       monoCheats,
	Injections:   "mono",
	Tiles: TileShape{TypeOff: 0x08, HeaderOff: 0x0E, ActiveBit: 0x20,
		Record: 24, DataOff: ArrDataOff, Resolver: "mono"},
	Projectiles: ProjectileShape{AIOff: ProjectileAI, LocalAIOff: ProjectileLocalAI,
		ActiveOff: ProjectileActive, BobberOff: ProjectileBobber,
		Len: ProjectileArrayLen, Resolver: "mono"},
	Provenance: "Cheat Engine mono dissector and cmd/monofields, 1.4.5.7 and 1.4.5.8",
}

/*
monoVerified is the build ledger the original anchors share: the build they were
derived against, and 1.4.5.8, where every anchor resolved and the maintainer
confirmed every cheat in play (2026-08-23). The full account was in
patch/anchors.go's verifiedBuilds, which these four left on 2026-10-09.
*/
var monoVerified = []string{Build1457s24825745, Build1458s24893155}

/*
monoAnchors are the patterns mono's in-place cheats resolve through, moved
unchanged from patch/anchors.go on 2026-10-09 (TestTheMonoCheatsAreWhatTheyWere
proves the move). See docs/discovery.md for how each was found.
*/
var monoAnchors = map[string]AnchorDef{
	/*
		ResetEffects: the blockRange reset (mov [edi+9F8],0 at +0), fld1 (+10)
		and the pickSpeed reset (fstp [edi+8D8] at +12) sit adjacent, so one
		anchor covers reach (patch offset 0) and mining (patch offset 12).

		The two reset instructions are wildcarded because those are exactly the
		bytes reach and mining overwrite: fixed, the anchor would stop matching
		once either cheat is applied, and a cold-cache re-resolve then failed
		with "anchor not found". Uniqueness comes from the invariant fld1 plus
		the downstream field-clear run, which no cheat touches.
	*/
	"reset_block": {
		Pattern: "?? ?? ?? ?? ?? ?? ?? ?? ?? ?? D9 E8 ?? ?? ?? ?? ?? ?? " +
			"C6 87 66 08 00 00 00 C6 87 70 08 00 00 00 C6 87 71 08 00 00 01",
		Verified: monoVerified,
	},
	/*
		ApplyItemTime(Item, float): the fmulp, cvttsd2si, max(edi,1) tail.

		fast_place overwrites the max(edi,1) at +20, turning `mov eax,1; cmp
		edi,eax; cmovl edi,eax` into `mov edi,4` and five nops, so those ten
		bytes are wildcarded -- otherwise a cold-cache re-resolve fails once the
		cheat is applied. The invariant prefix and the downstream store keep it
		unique.
	*/
	"place": {
		Pattern: "DE C9 DD 5D F0 F2 0F 10 45 F0 F2 0F 2C C8 8B F9 85 C0 7E 0A " +
			"?? ?? ?? ?? ?? ?? ?? ?? ?? ?? 89 7C 24 04 8B 45 08 89 04 24",
		Verified: monoVerified,
	},
	/*
		Player.ResetEffects: the per-frame `maxMinions = 1` reset.

		Its immediate, the reset value, is wildcarded so the anchor resolves
		whether or not the cap cheat is applied; uniqueness comes from the
		adjacent reset that follows it. The cheat rewrites the immediate to the
		cap wanted.
	*/
	"reset_minions": {
		Pattern:  "C7 87 F8 03 00 00 ?? ?? ?? ?? C7 87 60 0A 00 00 01 00 00 00",
		Verified: monoVerified,
	},
	/*
		TETeleportationPylon.PlacementPreviewHook_CheckIfCanPlace: the whole
		one-pylon-per-biome rule for single-player. Its IL is

			type = GetPylonTypeFromPylonTileStyle(style)
			return Main.PylonSystem.HasPylonOfType(type) ? 1 : 0

		and it is registered with badReturn 1, so returning 0 always lifts the
		limit. GetPylonTypeFromPylonTileStyle is inlined to the two movzx here.

		The first three bytes are the ones the cheat overwrites, so they are
		wildcarded -- otherwise a cold re-resolve fails once it is applied and it
		could not be turned off again. The mono type-init immediate, the init
		call, Main.PylonSystem's address and the call to HasPylonOfType are all
		ASLR'd.
	*/
	"pylon_place": {
		Pattern: "?? ?? ?? 83 EC 18 B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 " +
			"E8 ?? ?? ?? ?? 8B 45 14 0F B6 C0 0F B6 C8 8B 05 ?? ?? ?? ?? " +
			"89 4C 24 04 89 04 24 39 00 8D 6D 00 E8 ?? ?? ?? ??",
		// 2026-08-23: confirmed in-game on 1.4.5.8 -- a second Cavern pylon placed
		// beside the first, both on the map and wired into the network.
		Verified: []string{Build1458s24893155},
	},
}

// monoCheats are mono's in-place cheats, moved unchanged from patch/cheats.go.
var monoCheats = map[string]CheatSite{
	// fstp [edi+8D8] becomes fstp st(0) and four nops: the per-frame reset of
	// pickSpeed is popped and thrown away instead of stored.
	"mining": {Anchor: "reset_block", PatchOff: 12,
		Orig:    []byte{0xD9, 0x9F, 0xD8, 0x08, 0x00, 0x00},
		Patched: []byte{0xDD, 0xD8, 0x90, 0x90, 0x90, 0x90}},
	// The per-frame `blockRange = 0` reset, nopped out.
	"reach": {Anchor: "reset_block", PatchOff: 0,
		Orig:    []byte{0xC7, 0x87, 0xF8, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		Patched: []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90}},
	// The one-per-biome check becomes `xor eax,eax; ret`.
	"pylons": {Anchor: "pylon_place", PatchOff: 0,
		Orig:    []byte{0x55, 0x8B, 0xEC},
		Patched: []byte{0x31, 0xC0, 0xC3}},
	// `mov eax,1; cmp edi,eax; cmovl edi,eax` becomes `mov edi,N` and nops.
	"fast_place": {Anchor: "place", PatchOff: 20,
		Orig:    []byte{0xB8, 0x01, 0x00, 0x00, 0x00, 0x3B, 0xF8, 0x0F, 0x4C, 0xF8},
		Encoder: "mov-edi-imm32-min1"},
	// The immediate of `maxMinions = 1`.
	"max_minions": {Anchor: "reset_minions", PatchOff: 6,
		Orig:    []byte{0x01, 0x00, 0x00, 0x00},
		Encoder: "imm32-min1"},
}
