package patch

/*
Anchor is a pattern plus its provenance: the builds it has been seen working on.

Verification is a **ledger, not a gate**. An anchor that still matches on a build
nobody has confirmed is used anyway and the window marks it unproven, because
gating on the build id would disable working cheats: the 24825745 to 24893155
rebuild left seven of nine anchors matching with identical field displacements.

Unique marks the rare anchor where patching a structural twin would do harm.
Everything else patches every copy it finds, because mono can JIT one method into
more than one arena -- which is what broke reach, mining and the minion cap.

**Support accumulates.** When the game moves the code a cheat patches, the
re-derived bytes go in as a Variants entry *beside* the existing pattern, never
over it, so the older build keeps working. Replacing would silently break it
while Verified still claimed it. Builds seen so far have shared these bytes, so
there are no variants yet; the mechanism exists so that adding one is the obvious
move rather than a refactor.
*/
type Anchor struct {
	Pattern  Pattern
	Verified []string
	Unique   bool
	// Variants are builds where the game's code moved and this cheat needed
	// different bytes. Adding one never removes the pattern an older build
	// matches.
	Variants []Variant
}

// Variant is one build's own bytes for an anchor.
type Variant struct {
	Build   string
	Pattern Pattern
}

/*
Candidates is the patterns to try, best first.

The running build's own variant leads when there is one, and every other pattern
is still tried after it: an unverified build usually matches a pattern derived
for a different one, and gating on the build id would disable cheats that work.
Same reason Verified is a ledger and not a gate.
*/
func (a Anchor) Candidates(build string) []Pattern {
	var out []Pattern
	add := func(p Pattern) {
		for _, seen := range out {
			if seen.Equal(p) {
				return
			}
		}
		out = append(out, p)
	}
	if build != "" {
		for _, v := range a.Variants {
			if v.Build == build {
				add(v.Pattern)
				break
			}
		}
	}
	add(a.Pattern)
	for _, v := range a.Variants {
		add(v.Pattern)
	}
	return out
}

/*
The builds every anchor has been confirmed on, newest last.

"Verified" means the pattern resolved *and* the cheat was seen working in the
game on that build, not merely that it matched.

What a build key does not say is which .NET runtime was executing the game. These
patterns match machine code wine-mono's JIT emitted, so a Proton update can break
a cheat with Terraria untouched. The runtime is tracked beside the key rather
than folded into it: every entry here predates that tracking, and backfilling a
runtime nobody recorded would be a guess dressed as provenance.
*/
var verifiedBuilds = []string{
	// The build these AOBs were originally derived against.
	"1.4.5.7+24825745",
	/*
		2026-08-23, and a key to distrust: this one is a *mix*. The version came
		from the frequency vote in detect_version, which returns a stale 1.4.5.7
		even on 1.4.5.8, while the buildid came from Steam's already-updated
		manifest -- so the key describes a build that never existed. It is kept
		because the panel really did record verifications under it, and those
		were confirmed on 1.4.5.7; dropping it would silently un-verify them. The
		detector that produced it has since been fixed to read the version out of
		the exe the process maps.
	*/
	"1.4.5.7+24893155",
	/*
		2026-08-23, after the update was actually loaded: every anchor resolved
		on 1.4.5.8 and the maintainer confirmed all twelve cheats still working
		in-game. The update did not touch the code any of them patch.
	*/
	"1.4.5.8+24893155",
}

/*
verifiedInstead is the anchors with a different history to the list above,
derived later and so never seen on the original build.

An anchor that says nothing here claims every build in verifiedBuilds, so one
derived on 1.4.5.8 alone has to be listed even though it is verified on the
current build -- otherwise it silently claims two 1.4.5.7 builds it has never run
on. An empty list would mean "resolves, but not confirmed in-game anywhere",
which the window reports as unproven rather than hiding.
*/
var verifiedInstead = map[string][]string{
	/*
		2026-08-23: derived on this build and confirmed in-game with the whole
		vanity column occupied -- wings, Shield of Cthulhu, balloon and Hermes
		Boots all took effect, their Warding prefixes contributed defense (the
		equip_benefits edit), the vanity armour in 10 to 12 stayed inert, and the
		info accessories that already worked there were unchanged.
	*/
	"equip_apply":    {"1.4.5.7+24893155", "1.4.5.8+24893155"},
	"equip_benefits": {"1.4.5.7+24893155", "1.4.5.8+24893155"},
	/*
		2026-08-23: confirmed in-game -- accessories carried in the inventory
		granted their effects without being equipped, their Warding prefixes kept
		contributing defense when moved out of a vanity slot into the bag, and
		disabling restored the displaced bytes and stopped the effects with the
		game still running.
	*/
	"inventory_scan": {"1.4.5.7+24893155", "1.4.5.8+24893155"},
	/*
		2026-08-23: confirmed in-game with reach and tool reach at 75 -- the
		stutter, which merely holding Shift would trigger because it is the
		per-frame search and not the placing, is gone, while manual placement
		reach and tool reach are unchanged.
	*/
	"smart_cursor": {"1.4.5.7+24893155", "1.4.5.8+24893155"},
	/*
		2026-08-23: confirmed in-game on 1.4.5.8 -- a second Cavern pylon was
		placed with a Cavern pylon already in the world, and both appear on the
		map wired into the pylon network. Nothing downstream dedupes by type, as
		the recon predicted.
	*/
	"pylon_place": {"1.4.5.8+24893155"},
	/*
		2026-08-24: confirmed in-game on 1.4.5.8 -- the extractor calls PickTile
		through this for every tile it takes, and whole veins came out, 45 tiles
		in two batches, with the game healthy. Only ever derived on 1.4.5.8, so
		it claims nothing about 1.4.5.7.
	*/
	"pick_tile": {"1.4.5.8+24893155"},
	/*
		2026-08-24: Player.Update's per-frame call to GrabItems, where the
		extractor hooks. Derived and confirmed on 1.4.5.8 only.
	*/
	"grabitems_call": {"1.4.5.8+24893155"},
	/*
		2026-08-26: Player.Update's call to BordersMovement, where auto-use
		hooks. Derived and confirmed on 1.4.5.8 only -- 20 arms produced 20
		presses through this site, and two bites produced two fish. It claims
		nothing about 1.4.5.7.
	*/
	"borders_movement": {"1.4.5.8+24893155"},
}

// alsoVerified is per-anchor divergence, for a build that breaks only some
// anchors. There is none at present; the shape is here so adding one is an
// entry rather than a refactor.
var alsoVerified = map[string][]string{}

// rawAnchors is every pattern, under the name the rest of the program calls it
// by. See docs/discovery.md for how each was found.
var rawAnchors = map[string]Pattern{
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
	"reset_block": MustParse(
		"?? ?? ?? ?? ?? ?? ?? ?? ?? ?? D9 E8 ?? ?? ?? ?? ?? ?? " +
			"C6 87 66 08 00 00 00 C6 87 70 08 00 00 00 C6 87 71 08 00 00 01"),

	/*
		Player.Update's call to BordersMovement: the auto-use hook. The only call
		site of that method in Update, unconditional, and about 50 IL bytes
		before ItemCheckWrapped reads the use control, so a write here lands just
		before the read.

		The 13 bytes after the call are the patch site and are wildcarded. Unlike
		grabitems_call this anchor stays unique that way, because the trailing
		fstp of slotsMinions and the Main.netMode compare carry the uniqueness on
		their own.
	*/
	"borders_movement": MustParse(
		"E8 ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? ?? " +
			"8B 45 08 D9 EE D9 98 ?? ?? ?? ?? 8B 05 ?? ?? ?? ?? " +
			"3D 02 00 00 00"),

	/*
		Player.Update's call to GrabItems: the per-frame site the ore extractor
		hooks.

		The pattern covers the `if (!dead)` check that guards the call plus the
		argument setup, so it is anchored to the real call rather than to an
		incidental byte sequence -- the argument-setup tail alone matches 154
		places. The field displacement is wildcarded, and so are the five bytes
		at +21 that the extractor overwrites: those carry no relative address,
		which is what makes them displaceable. The call itself could not be, its
		rel32 differs every session.
	*/
	"grabitems_call": MustParse(
		"0F B6 80 ?? ?? ?? ?? 85 C0 75 14 8B 45 08 8B 4D 0C " +
			"89 4C 24 04 ?? ?? ?? ?? ??"),

	/*
		Player.PickTile's entry. The first five bytes are the ones the cheat
		overwrites with its jump, so they are wildcarded -- the trap that specs
		032 to 034 and 037 each hit.

		The prologue alone is far from unique, 190 methods share it, so the
		pattern runs on through the argument loads, the mono type-init check and
		the two zeroed locals to the `mov eax,[Main.tile]`. That is unique.
	*/
	"pick_tile": MustParse(
		"?? ?? ?? ?? ?? 56 83 EC 7C 8B 7D 08 8B 5D 0C B8 ?? ?? ?? ?? " +
			"F7 00 01 00 00 00 74 08 8D 6D 00 E8 ?? ?? ?? ?? " +
			"C7 45 E4 00 00 00 00 C7 45 E0 00 00 00 00 8B 05 ?? ?? ?? ??"),

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
	"pylon_place": MustParse(
		"?? ?? ?? 83 EC 18 B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 " +
			"E8 ?? ?? ?? ?? 8B 45 14 0F B6 C0 0F B6 C8 8B 05 ?? ?? ?? ?? " +
			"89 4C 24 04 89 04 24 39 00 8D 6D 00 E8 ?? ?? ?? ??"),

	/*
		ApplyItemTime(Item, float): the fmulp, cvttsd2si, max(edi,1) tail.

		fast_place overwrites the max(edi,1) at +20, turning `mov eax,1; cmp
		edi,eax; cmovl edi,eax` into `mov edi,4` and five nops, so those ten
		bytes are wildcarded -- otherwise a cold-cache re-resolve fails once the
		cheat is applied. The invariant prefix and the downstream store keep it
		unique.
	*/
	"place": MustParse(
		"DE C9 DD 5D F0 F2 0F 10 45 F0 F2 0F 2C C8 8B F9 85 C0 7E 0A " +
			"?? ?? ?? ?? ?? ?? ?? ?? ?? ?? 89 7C 24 04 8B 45 08 89 04 24"),

	/*
		TileReachCheckSettings.GetRanges(this, out x, out y): prologue, mono
		type-init, the tileRangeX read and the first imul and store, with the
		ASLR'd immediates wildcarded to make it unique. It starts at the method
		base, so the injection offset of 0xCA is measured from here.
	*/
	"getranges": MustParse(
		"55 8B EC 53 57 56 83 EC 1C 8B 5D 08 8B 75 0C 8B 7D 10 " +
			"B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 E8 ?? ?? ?? ?? " +
			"8B 05 ?? ?? ?? ?? 8B 0B 0F AF C1 89 06"),

	/*
		Player.GrabItems: the grab-range store, then the first get_Hitbox call
		with its rel32 wildcarded. It starts at the injection point, so the
		injection offset is 0.

		A known exception to the rule that a patch site is wildcarded. These six
		bytes are the ones the pickup injection overwrites, but they are also
		what makes this anchor unique: wildcarding them takes it from one live
		site to 124. Until the pattern is re-cut with enough trailing context to
		stand on its own, pickup keeps a literal patch site and its cold-cache
		status stays unreliable. That is the lesser bug -- 124 candidate sites is
		not a status problem, it is a corrupted game.
	*/
	"grabitems": MustParse("89 45 AC 8D 45 B0 89 44 24 04 89 1C 24 39 1B E8 ?? ?? ?? ??"),

	/*
		Spawner.GetSpawnRate's prologue: esi is the out spawnRate and edi the out
		maxSpawns, then fldz, fstp and the first `mov [esi],[static]`. The same
		esi and edi out shape as getranges, and forced at the epilogue.
	*/
	"get_spawn_rate": MustParse(
		"55 8B EC 53 57 56 83 EC 5C 8B 5D 0C 8B 75 10 8B 7D 14 " +
			"B8 ?? ?? ?? ?? F7 00 01 00 00 00 74 05 E8 ?? ?? ?? ?? " +
			"D9 EE D9 5D D4 8B 05 ?? ?? ?? ?? 89 06"),

	/*
		CommonDrop.TryDroppingItem, where esi is the CommonDrop.

		The chance roll passes chanceDenominator as the RNG bound, and a drop
		happens when the roll is below chanceNumerator; capping the denominator
		floors the drop chance. The anchor is based downstream of that, on the
		tail that reads chanceNumerator and itemId, so the pattern sits
		*entirely* past the bytes the jump overwrites. That keeps it scannable
		after the patch is in place, so disabling never hits a self-corrupted
		seed. The call-target immediates are wildcarded, which also makes it
		match both CommonDrop twins.
	*/
	"trydrop": MustParse(
		"89 04 24 39 00 90 E8 ?? ?? ?? ?? 8B 4E 1C 3B C1 0F 8D ?? ?? ?? ?? " +
			"8B 45 10 89 45 E8 8B 46 0C 89 45 E4"),

	/*
		Main.TriggerPing(Vector2 position), which fires when a fullscreen-map
		ping is placed; [ebp+08] and [ebp+0C] are the ping's world X and Y.

		The anchor is the position-independent argument-marshal tail, with the
		following call's rel32 wildcarded. The overwrite is the first seven
		bytes, reproduced in the stub, so they are wildcarded too. It is placed
		here rather than at the class-init `mov eax,<imm>` because that
		instruction embeds an ASLR'd address and would not be a stable overwrite.
	*/
	"trigger_ping": MustParse(
		"?? ?? ?? ?? ?? ?? ?? 8B 4D 0C 89 4C 24 08 89 04 24 39 00 " +
			"8D 6D 00 E8 ?? ?? ?? ??"),

	/*
		Player.Teleport(Vector2 newPos, int Style, int extraInfo): the call
		target for the map-ping hook.

		Anchored on the two constant field stores at Teleport+0x32 plus the Style
		dispatch, all position-independent. The method entry, which is the
		absolute call target, is the anchor base minus 0x32.
	*/
	"player_teleport": MustParse(
		"C7 83 F4 0B 00 00 64 00 00 00 C7 83 68 04 00 00 04 00 00 00 " +
			"83 FE 0A 0F 94 C0 0F B6 C0"),

	/*
		Player.ResetEffects: the per-frame `maxMinions = 1` reset.

		Its immediate, the reset value, is wildcarded so the anchor resolves
		whether or not the cap cheat is applied; uniqueness comes from the
		adjacent reset that follows it. The cheat rewrites the immediate to the
		cap wanted.
	*/
	"reset_minions": MustParse("C7 87 F8 03 00 00 ?? ?? ?? ?? C7 87 60 0A 00 00 01 00 00 00"),

	/*
		UpdateEquips' accessory-effect loop:

			for (k = 3; k < 10; k++)
			    if (IsItemSlotUnlockedAndUsable(k))
			        ApplyEquipFunctional(k, GetEffectiveArmor(k))

		Matched from the item argument store through the loop bound, so one
		anchor covers both edits the vanity cheat needs. The slot argument is
		already in eax when it is stored, which is what lets the clamp stub avoid
		any assumption about the frame layout.

		Wildcarded: the ebp displacements and call rel32s, the seven bytes the
		stub displaces, and the loop bound itself -- a cold re-resolve must still
		match once the cheat is applied. See ce/ACCESSORY_FINDINGS.md.
	*/
	"equip_apply": MustParse(
		"89 44 24 08 8B 45 ?? ?? ?? ?? ?? ?? ?? ?? 90 E8 ?? ?? ?? ?? " +
			"83 45 ?? 01 83 7D ?? ?? 7C ??"),

	/*
		SmartCursorHelper.SmartCursorLookup, at the tail where the search box it
		has just got from GetTileRegion is clamped against the world edges: four
		load, clamp and store blocks writing SmartCursorUsageInfo through esi.

		Matched from the StartY block through the final EndY store, which is what
		the cheat displaces, together with the `test ebx,ebx` whose flags the
		following je needs. See ce/SMARTCURSOR_FINDINGS.md.
	*/
	"smart_cursor": MustParse(
		"8B 46 38 8B 0D ?? ?? ?? ?? 83 E9 0A 89 4C 24 08 " +
			"C7 44 24 04 0A 00 00 00 89 04 24 90 E8 ?? ?? ?? ?? 89 46 38 " +
			"8B 46 3C 8B 0D ?? ?? ?? ?? 83 E9 0A 89 4C 24 08 " +
			"C7 44 24 04 0A 00 00 00 89 04 24 90 E8 ?? ?? ?? ?? " +
			"?? ?? ?? ?? ?? 74 ??"),

	/*
		UpdateEquips' first loop, which already walks the whole inventory every
		frame -- vanilla uses it to refresh info and mechanical accessories,
		which is why a Depth Meter works from the bag today.

		Matched from the Player.inventory read, the same field the inventory
		package reaches at statLife-0x664, through the bounds check and the
		element address, to the point where the Item pointer is in eax. The five
		bytes the stub displaces are wildcarded: a cold re-resolve has to work
		while the cheat is applied.
	*/
	"inventory_scan": MustParse(
		"8B 87 D4 00 00 00 8B 4D ?? 39 48 0C 0F 86 ?? ?? ?? ?? " +
			"8D 44 88 10 ?? ?? ?? ?? ?? 89 45 ??"),

	/*
		UpdateEquips' benefit loop:

			if (item.accessory) GrantPrefixBenefits(item);
			GrantArmorBenefits(item)

		and its `i < 10` bound, wildcarded because the cheat patches it. The
		accessory test is what makes this specific -- the bare increment and
		compare tail matches unrelated code elsewhere in the process.
	*/
	"equip_benefits": MustParse(
		"0F B6 40 7D 85 C0 74 ?? 8B 45 ?? 89 44 24 04 89 3C 24 8B C0 " +
			"E8 ?? ?? ?? ?? 8B 45 ?? 89 44 24 04 89 3C 24 90 E8 ?? ?? ?? ?? " +
			"83 45 ?? 01 83 7D ?? ??"),
}

// Anchors is every pattern with the provenance that belongs to it.
var Anchors = buildAnchors()

// buildAnchors attaches each pattern's verified builds, which is the list it
// says instead when it has one, and otherwise every build plus whatever
// divergence is recorded for it.
func buildAnchors() map[string]Anchor {
	out := make(map[string]Anchor, len(rawAnchors))
	for key, pat := range rawAnchors {
		verified, instead := verifiedInstead[key]
		if !instead {
			verified = append(append([]string{}, verifiedBuilds...), alsoVerified[key]...)
		}
		out[key] = Anchor{Pattern: pat, Verified: verified}
	}
	return out
}
