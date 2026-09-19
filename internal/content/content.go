/*
Package content is every item's and NPC's default stats, read from the game's own
template objects.

Stats like damage, defense and rarity are not in the game's executable in any
readable form -- they are assigned at runtime. The authority is the game's sample
collection, which holds one fully-populated object per type.

That collection is a managed dictionary, and walking one means depending on the
*runtime's* field layout rather than the game's, which is a failure the build
ledger would not catch cleanly. So the templates are found a different way: scan
the writable memory for objects carrying the right vtable, and pick the template
table out by its shape.

The table gives itself away by being **one object per type**. Live objects --
inventories, chests, dropped items -- repeat types heavily, so a run of addresses
holding roughly as many objects as distinct types is a template table. Measured
on a live game: 169,637 item-shaped objects, of which the one-to-one runs cover
6,162 distinct types, which is all of them.

Ported from terrariabonker/content.py (spec 051, step 5).
*/
package content

import (
	"encoding/binary"
	"sort"
	"strings"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
ClusterGap is how far apart two addresses have to be to start a new run.

Chosen from live data: it merges the template table's chunks without swallowing
the surrounding heap of live items.
*/
const ClusterGap = 0x400000

// MaxType bounds a plausible item id: a stale pointer read as an object gives an
// absurd one.
const MaxType = 7000

// Mem is the memory the templates are scanned out of.
type Mem interface {
	Regions() []proc.Region
	Read(addr uint32, size int) []byte
}

// ItemStats is one item's defaults.
type ItemStats struct {
	Type    int32 `json:"type"`
	Damage  int32 `json:"damage"`
	Defense int32 `json:"defense"`
	Rare    int32 `json:"rare"`
	Pick    int32 `json:"pick"`
	UseTime int32 `json:"use_time"`
	// UseAnim is needed to tell a real edit from an item's own defaults.
	UseAnim    int32 `json:"use_anim"`
	TileBoost  int32 `json:"tile_boost"`
	AutoReuse  int32 `json:"auto_reuse"`
	CreateTile int32 `json:"create_tile"`
	BuffType   int32 `json:"buff_type"`
	HealLife   int32 `json:"heal_life"`
	HealMana   int32 `json:"heal_mana"`
	HeadSlot   int32 `json:"head_slot"`
	BodySlot   int32 `json:"body_slot"`
	LegSlot    int32 `json:"leg_slot"`
	/*
		Prefix is kept only to tell a modified copy from a pristine one, and
		never leaves this package: a template by definition carries none, so
		reporting it would be reporting a field that is always zero and inviting
		somebody to restore it.
	*/
	Prefix    int32 `json:"-"`
	Accessory bool  `json:"accessory"`
	Melee     bool  `json:"melee"`
	Ranged    bool  `json:"ranged"`
	Magic     bool  `json:"magic"`
	Summon    bool  `json:"summon"`
}

// found is one object the scan turned up.
type found[T any] struct {
	addr  uint32
	stats T
}

/*
scanObjects is every object carrying a vtable, in address order.

read turns one object into its stats; accept rejects the false positives a bare
vtable match always produces, because a stale pointer in the middle of some other
structure reads as an object with an absurd type.
*/
func scanObjects[T any](mem Mem, vtable uint32, span int,
	read func([]byte, int) T, accept func(T) bool) []found[T] {
	var out []found[T]
	for _, region := range mem.Regions() {
		buf := mem.Read(region.Start, region.Size())
		for off := 0; off+4 <= len(buf); off += 4 {
			if binary.LittleEndian.Uint32(buf[off:]) != vtable {
				continue
			}
			if off+span > len(buf) {
				continue
			}
			stats := read(buf, off)
			if accept(stats) {
				out = append(out, found[T]{addr: region.Start + uint32(off), stats: stats}) //nolint:gosec // an offset in a 32-bit region
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out
}

/*
templateTable picks the template table out of every object of a class, keyed by
whatever tells one template from another.

The table gives itself away by being one object per key: live objects repeat
their keys heavily, templates never do. Addresses are clustered into runs, and
from each run the keys appearing in it **exactly once** are kept. Bigger runs are
consulted first, so a key appearing in more than one run is taken from the real
table rather than from a chest that happens to look one-to-one.

Judging keys rather than whole runs matters. Scoring a run as a whole and
discarding it when it was not near enough to one-to-one lost real templates to
whatever happened to be allocated beside them: seven variants of one NPC sit next
to seven default-state objects, which made their run fourteen objects for eight
distinct keys and threw away all seven. Per key, the duplicated key drops out and
its neighbours survive.
*/
func templateTable[T any](objects []found[T], key func(T) int32) map[int32]T {
	out := map[int32]T{}
	if len(objects) == 0 {
		return out
	}
	runs := [][]found[T]{{objects[0]}}
	for _, o := range objects[1:] {
		last := runs[len(runs)-1]
		if o.addr-last[len(last)-1].addr > ClusterGap {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], o)
	}
	sort.SliceStable(runs, func(i, j int) bool { return len(runs[i]) > len(runs[j]) })

	for _, run := range runs {
		seen := map[int32]int{}
		for _, o := range run {
			seen[key(o.stats)]++
		}
		for _, o := range run {
			k := key(o.stats)
			if seen[k] != 1 {
				continue
			}
			if _, already := out[k]; !already {
				out[k] = o.stats
			}
		}
	}
	return out
}

/*
consensus is the stats a *pristine* copy of an item has.

Two rules, in order. A modifier changes damage and use time, so a prefixed copy
is not a template: where any copy is unprefixed, only those are considered. Then
the most common stat tuple wins -- a pristine template agrees with every other
pristine copy of its type, while an edited one stands alone.

Choosing by position instead let a live, modified item be returned as the
template. It reported the maintainer's own edits as one weapon's base stats, and
those were then used as the baseline for deciding which saved edits were
redundant, destroying eight of them.
*/
func consensus(candidates []ItemStats) (ItemStats, bool) {
	if len(candidates) == 0 {
		return ItemStats{}, false
	}
	var clean []ItemStats
	for _, c := range candidates {
		if c.Prefix == 0 {
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		clean = candidates
	}
	if len(clean) == 1 {
		return clean[0], true
	}
	tally := map[ItemStats]int{}
	for _, c := range clean {
		tally[withoutPrefix(c)]++
	}
	/*
		Walked in the order the copies were found rather than over the tally, so
		that equally-common candidates are broken the same way every time and the
		same way the Python breaks them -- first seen wins.

		Ranging over the tally instead reads a Go map, whose order is deliberately
		random: two copies of one type that disagree would then produce a
		different template on different runs of the same program, against the
		same game. That is worse than picking the wrong one, because it cannot be
		reproduced.
	*/
	best, most := clean[0], 0
	for _, c := range clean {
		if n := tally[withoutPrefix(c)]; n > most {
			best, most = c, n
		}
	}
	return best, true
}

// withoutPrefix is a copy with the modifier tier cleared, which is the only
// field a template is allowed to disagree on.
func withoutPrefix(s ItemStats) ItemStats {
	s.Prefix = 0
	return s
}

// itemSpan is how much of an item object has to be readable before its fields
// can be trusted.
var itemSpan = furthest(layout.ItemType, layout.ItemDamage, layout.ItemDefense,
	layout.ItemRare, layout.ItemPick, layout.ItemUseTime, layout.ItemCreateTile,
	layout.ItemAccessory, layout.ItemSummon, layout.ItemLegSlot, layout.ItemHealMana,
	layout.ItemBuffType, layout.ItemUseAnim, layout.ItemTileBoost, layout.ItemAutoReuse,
	layout.ItemPrefix) + 4

// furthest is the largest of some offsets.
func furthest(offs ...int) int {
	most := 0
	for _, o := range offs {
		if o > most {
			most = o
		}
	}
	return most
}

/*
FindItemTemplates is every item the game has a template for.

exclude is the addresses of objects known to be the player's own -- the ones this
program edits, and so the likeliest to be mistaken for a template.
*/
func FindItemTemplates(mem Mem, vtable uint32, exclude map[uint32]bool) map[int32]ItemStats {
	objects := scanObjects(mem, vtable, itemSpan, readItemStats,
		func(s ItemStats) bool { return s.Type > 0 && s.Type < MaxType })

	byType := map[int32][]ItemStats{}
	for _, o := range objects {
		if exclude[o.addr] {
			continue
		}
		byType[o.stats.Type] = append(byType[o.stats.Type], o.stats)
	}
	out := make(map[int32]ItemStats, len(byType))
	for t, candidates := range byType {
		if got, ok := consensus(candidates); ok {
			out[t] = withoutPrefix(got)
		}
	}
	return out
}

// readItemStats is one item object's fields.
func readItemStats(buf []byte, off int) ItemStats {
	i32 := func(field int) int32 {
		return int32(binary.LittleEndian.Uint32(buf[off+field:])) //nolint:gosec // a field, as its bits
	}
	return ItemStats{
		Type: i32(layout.ItemType), Damage: i32(layout.ItemDamage),
		Defense: i32(layout.ItemDefense), Rare: i32(layout.ItemRare),
		Pick: i32(layout.ItemPick), UseTime: i32(layout.ItemUseTime),
		UseAnim: i32(layout.ItemUseAnim), TileBoost: i32(layout.ItemTileBoost),
		AutoReuse:  int32(buf[off+layout.ItemAutoReuse]),
		CreateTile: i32(layout.ItemCreateTile), BuffType: i32(layout.ItemBuffType),
		HealLife: i32(layout.ItemHealLife), HealMana: i32(layout.ItemHealMana),
		HeadSlot: i32(layout.ItemHeadSlot), BodySlot: i32(layout.ItemBodySlot),
		LegSlot: i32(layout.ItemLegSlot), Prefix: int32(buf[off+layout.ItemPrefix]),
		Accessory: buf[off+layout.ItemAccessory] != 0,
		Melee:     buf[off+layout.ItemMelee] != 0,
		Ranged:    buf[off+layout.ItemRanged] != 0,
		Magic:     buf[off+layout.ItemMagic] != 0,
		Summon:    buf[off+layout.ItemSummon] != 0,
	}
}

// NPCStats is one NPC's defaults.
type NPCStats struct {
	Type    int32 `json:"type"`
	NetID   int32 `json:"net_id"`
	Life    int32 `json:"life"`
	Damage  int32 `json:"damage"`
	Defense int32 `json:"defense"`
	Width   int32 `json:"width"`
	Height  int32 `json:"height"`
	Boss    bool  `json:"boss"`
	Town    bool  `json:"town"`
	// Color is the tint the game paints a neutral sheet with. All zero means no
	// tint.
	Color [4]byte `json:"color"`
}

// readNPCStats is one NPC object's fields.
func readNPCStats(buf []byte, off int) NPCStats {
	i32 := func(field int) int32 {
		return int32(binary.LittleEndian.Uint32(buf[off+field:])) //nolint:gosec // a field, as its bits
	}
	var color [4]byte
	copy(color[:], buf[off+layout.NPCColor:off+layout.NPCColor+4])
	return NPCStats{
		Type: i32(layout.NPCType), NetID: i32(layout.NPCNetID),
		Life: i32(layout.NPCLifeMax), Damage: i32(layout.NPCDamage),
		Defense: i32(layout.NPCDefense), Width: i32(layout.NPCWidth),
		Height: i32(layout.NPCHeight),
		Boss:   buf[off+layout.NPCBoss] != 0, Town: buf[off+layout.NPCTown] != 0,
		Color: color,
	}
}

/*
FindNPCTemplates is every NPC the game has a template for, keyed by net id.

Keyed on the net id rather than the type because the game's own collection is:
the variants share a type and are told apart only by a negative net id, and those
are exactly the names the bundled table carries.

Reading the templates rather than the NPCs in the world is not a detail -- a live
NPC's stats have been scaled by the world's difficulty, so a Blue Slime in an
expert world reads 60 life where its template says 25.
*/
func FindNPCTemplates(mem Mem, vtable uint32, exclude map[uint32]bool) map[int32]NPCStats {
	objects := scanObjects(mem, vtable, layout.NPCObjectSize, readNPCStats,
		func(s NPCStats) bool {
			return s.NetID > -layout.MaxNPCType && s.NetID < layout.MaxNPCType &&
				s.Type >= 0 && s.Type < layout.MaxNPCType
		})
	kept := objects[:0]
	for _, o := range objects {
		if !exclude[o.addr] {
			kept = append(kept, o)
		}
	}
	return templateTable(kept, func(s NPCStats) int32 { return s.NetID })
}

/*
ItemKind is a one-word category for the compendium, from the fields this project
already reads.

Ordered most-specific first: an accessory that also deals damage is still an
accessory, and a pickaxe that deals damage is still a tool.
*/
func ItemKind(s ItemStats) string {
	switch {
	case s.Accessory:
		return "Accessory"
	// A slot with no defense is still worn, so vanity counts as armour.
	case s.HeadSlot >= 0 || s.BodySlot >= 0 || s.LegSlot >= 0:
		return "Armor"
	case s.Pick > 0:
		return "Tool"
	case s.Damage > 0:
		// Before the buff check: a summon staff grants its minion as a buff and
		// is a weapon, not a potion.
		switch {
		case s.Summon:
			return "Summon"
		case s.Magic:
			return "Magic"
		case s.Ranged:
			return "Ranged"
		}
		return "Weapon"
	// Buff potions carry no healing at all, which is why filtering on healing
	// alone showed only restoratives. Food grants a buff too and lands here.
	case s.HealLife > 0 || s.HealMana > 0 || s.BuffType > 0:
		return "Potion"
	case s.Defense > 0:
		return "Armor"
	case s.CreateTile >= 0:
		return "Block"
	}
	return "Material"
}

/*
NPCKind is boss, town NPC, critter or monster, from the template's own fields.

Boss and town are real flags on the object. "Critter" is not -- nothing on the
template says critter in a way this project could confirm, so it is defined by
what is observable: an NPC that deals no damage. That puts the bunnies and birds
where a player expects them, and misfiles the target dummy, which is an
acceptable price for not inventing a flag.
*/
func NPCKind(s NPCStats) string {
	switch {
	case s.Boss:
		return "Boss"
	case s.Town:
		return "Town NPC"
	case s.Damage <= 0:
		return "Critter"
	}
	return "Monster"
}

/*
WikiURL is the official wiki article for a display name.

Opened in the user's browser rather than fetched: this program makes no network
requests. Article titles use underscores, and a name that redirects or misses
lands on the wiki's own search, which is an acceptable outcome for the handful
that differ.
*/
func WikiURL(name string) string {
	return "https://terraria.wiki.gg/wiki/" + strings.Join(strings.Fields(name), "_")
}
