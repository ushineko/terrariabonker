package content_test

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
The templates are what "what does this item normally look like" means.

Everything downstream leans on them: which saved edits are worth restoring, what
a modifier scales from, what the compendium shows. Reading a *live, edited* item
as a template once reported the maintainer's own edits as a weapon's base stats,
and the next thing along used those as the baseline for deciding which saved
edits were redundant and destroyed eight of them.

So the whole picking-out is compared: the scan, the clustering, and which copy of
a type wins.
*/

const (
	base   = 0x10000000
	size   = 0x500000 // wide enough to hold two runs more than a cluster gap apart
	vtable = 0xDEADBEEF
)

const pythonTimeout = 2 * time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// item is one planted object: where it goes, and the fields that matter.
type item struct {
	at     uint32
	fields map[string]int32
}

/*
itemImage is the objects planted for the item scan.

It holds every case the picking-out has to get right: a type with one copy, a
type whose copies disagree because one was edited, a type whose *only* copy is
prefixed, an object with an absurd type that a stale pointer would produce, and
one the caller says is the player's own.
*/
var itemImage = []item{
	// A lone template.
	{at: base + 0x1000, fields: map[string]int32{"ITEM_TYPE": 3509, "ITEM_DAMAGE": 8, "ITEM_PICK": 35}},
	// Three copies of one type, the *first* of them edited. The two that agree
	// win, which is only visible because the odd one out is found first: with it
	// last, taking the first copy would give the same answer by accident.
	{at: base + 0x1200, fields: map[string]int32{"ITEM_TYPE": 4, "ITEM_DAMAGE": 500, "ITEM_MELEE": 1}},
	{at: base + 0x1400, fields: map[string]int32{"ITEM_TYPE": 4, "ITEM_DAMAGE": 12, "ITEM_MELEE": 1}},
	{at: base + 0x1600, fields: map[string]int32{"ITEM_TYPE": 4, "ITEM_DAMAGE": 12, "ITEM_MELEE": 1}},
	// Two copies, one of them carrying a modifier. The unprefixed one wins even
	// though it is outnumbered by nothing -- a prefixed copy is not a template.
	{at: base + 0x1800, fields: map[string]int32{"ITEM_TYPE": 757, "ITEM_DAMAGE": 40, "ITEM_PREFIX": 81}},
	{at: base + 0x1A00, fields: map[string]int32{"ITEM_TYPE": 757, "ITEM_DAMAGE": 35}},
	/*
		Two copies that disagree and neither carries a modifier, so nothing
		distinguishes them but the order they were found in.

		A real tie, and the point of having one: the answer has to be the same on
		every run and the same as the Python's, which takes the first. Picking by
		walking a tally reads a Go map, whose order is deliberately random.
	*/
	{at: base + 0x1B00, fields: map[string]int32{"ITEM_TYPE": 1234, "ITEM_DAMAGE": 10}},
	{at: base + 0x1B80, fields: map[string]int32{"ITEM_TYPE": 1234, "ITEM_DAMAGE": 20}},
	// A stale pointer read as an object: the type is not one.
	{at: base + 0x1C00, fields: map[string]int32{"ITEM_TYPE": 999999}},
	// The player's own, which the caller excludes by address.
	{at: base + 0x1E00, fields: map[string]int32{"ITEM_TYPE": 9, "ITEM_DAMAGE": 77}},
	{at: base + 0x2000, fields: map[string]int32{"ITEM_TYPE": 9, "ITEM_DAMAGE": 0}},
}

// excluded is the address the caller says belongs to the player.
const excluded = base + 0x1E00

/*
npcImage is the objects planted for the NPC scan, in two runs far enough apart to
cluster separately.

The big run is the template table. The small one stands for a handful of live
NPCs, and it repeats a net id -- which is what makes it not a table, and what the
per-key rule has to drop without taking its neighbours with it.
*/
var npcImage = []item{
	{at: base + 0x10000, fields: map[string]int32{"NPC_NET_ID": 1, "NPC_TYPE": 1, "NPC_LIFE_MAX": 25}},
	{at: base + 0x10300, fields: map[string]int32{"NPC_NET_ID": -2, "NPC_TYPE": 1, "NPC_LIFE_MAX": 25}},
	{at: base + 0x10600, fields: map[string]int32{"NPC_NET_ID": 2, "NPC_TYPE": 2, "NPC_LIFE_MAX": 40, "NPC_BOSS": 1}},
	{at: base + 0x10900, fields: map[string]int32{"NPC_NET_ID": 3, "NPC_TYPE": 3, "NPC_LIFE_MAX": 5}},
	{at: base + 0x10C00, fields: map[string]int32{"NPC_NET_ID": 4, "NPC_TYPE": 4, "NPC_LIFE_MAX": 250, "NPC_TOWN": 1}},
	// Also in the smaller run below, and unique in both. The bigger run is the
	// real table, so its reading is the one that has to win.
	{at: base + 0x10F00, fields: map[string]int32{"NPC_NET_ID": 6, "NPC_TYPE": 6, "NPC_LIFE_MAX": 11}},
	// A separate run: two live copies of one net id, and a neighbour that is
	// unique inside it. The duplicate drops, the neighbour survives.
	{at: base + 0x420000, fields: map[string]int32{"NPC_NET_ID": 5, "NPC_TYPE": 5, "NPC_LIFE_MAX": 60}},
	{at: base + 0x420300, fields: map[string]int32{"NPC_NET_ID": 5, "NPC_TYPE": 5, "NPC_LIFE_MAX": 60}},
	// The same net id as in the table above, with the difficulty-scaled life a
	// live NPC has.
	{at: base + 0x420600, fields: map[string]int32{"NPC_NET_ID": 6, "NPC_TYPE": 6, "NPC_LIFE_MAX": 60}},
}

// plant writes the objects into a Go fake, each behind the vtable the scan looks
// for.
func plant(objects []item) *memtest.FakeMem {
	mem := memtest.New(base, size)
	for _, o := range objects {
		mem.PokeBytes(o.at, u32(vtable))
		for name, v := range o.fields {
			mem.PokeI32(o.at+uint32(layout.Offsets[name]), v) //nolint:gosec // an offset from the table
		}
	}
	return mem
}

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

/*
The templates picked out of the planted heap are the ones the image says they
are.

The image carries the cases the rule exists for: two copies of one type where
only one is pristine, a copy the caller excludes because it is the player's own,
and a run laid out so a wrong clustering would take the wrong one.
*/
func TestFindingItemTemplates(t *testing.T) {
	got := content.FindItemTemplates(plant(itemImage), vtable, map[uint32]bool{excluded: true})

	require.Len(t, got, 5, "a different number of templates was found")
	require.Contains(t, got, int32(1234))
	require.Equal(t, int32(10), got[1234].Damage, "the pristine copy did not win")
	require.Contains(t, got, int32(9))
	require.Equal(t, int32(0), got[9].Damage,
		"the player's own edited copy was taken as the template")
}

/*
An item whose only copy is excluded has no template at all.

The excluded address is the player's own item, and the other copy of that type is
a second one they carry -- so the type survives, with the stats of the copy that
was not excluded rather than the one that was.
*/
func TestAnExcludedItemDoesNotBecomeATemplate(t *testing.T) {
	got := content.FindItemTemplates(plant(itemImage), vtable, map[uint32]bool{excluded: true})
	require.Contains(t, got, int32(9))
	require.Equal(t, int32(0), got[9].Damage,
		"the player's own edited copy was taken as the template")

	// The tie resolves the same way however many times it is asked.
	for range 20 {
		again := content.FindItemTemplates(plant(itemImage), vtable, nil)
		require.Equal(t, got[1234].Damage, again[1234].Damage,
			"a tie between two copies resolves differently from one run to the next")
	}

	// With nothing excluded, the edited copy is a candidate again.
	all := content.FindItemTemplates(plant(itemImage), vtable, nil)
	require.Contains(t, all, int32(9))
}

/*
The NPC templates cluster the same way, and a duplicate drops without taking its
neighbour with it.

A net id that appears twice in one run is not a template -- the collection holds
one object per id -- and dropping the whole run over it would lose every NPC
beside it.
*/
func TestFindingNPCTemplates(t *testing.T) {
	got := content.FindNPCTemplates(plant(npcImage), vtable, nil)

	require.NotContains(t, got, int32(5), "a net id that appears twice in a run was kept")
	require.Contains(t, got, int32(6), "a unique neighbour was thrown away with the duplicate")
	require.Equal(t, int32(11), got[6].Life,
		"a live NPC's difficulty-scaled life was taken for its template")

	require.Contains(t, got, int32(-2), "a variant's negative net id was not kept")
	require.Equal(t, int32(1), got[-2].Type, "the variant does not name its own type")
	require.Equal(t, int32(25), got[-2].Life)
}

/*
Every item is filed under one word, most specific first.

An accessory that also deals damage is still an accessory, and a pickaxe that
deals damage is still a tool -- the order of the rules is the rule.
*/
func TestItemKind(t *testing.T) {
	for _, c := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{"accessory": true, "damage": 50}, "Accessory"},
		{map[string]any{"head_slot": 98}, "Armor"},
		// Vanity is still worn, so a slot with no defense is still armour.
		{map[string]any{"head_slot": -1, "body_slot": -1, "leg_slot": 0}, "Armor"},
		{map[string]any{"pick": 35, "damage": 4}, "Tool"},
		{map[string]any{"damage": 12, "melee": true}, "Weapon"},
		{map[string]any{"damage": 20, "magic": true}, "Magic"},
		{map[string]any{"damage": 15, "ranged": true}, "Ranged"},
		// A staff grants a buff and is not a potion.
		{map[string]any{"damage": 30, "summon": true, "buff_type": 40}, "Summon"},
		{map[string]any{"heal_life": 100}, "Potion"},
		// A buff potion heals nothing and is still a potion.
		{map[string]any{"buff_type": 11}, "Potion"},
		{map[string]any{"defense": 4}, "Armor"},
		{map[string]any{"create_tile": 0}, "Block"},
		{map[string]any{"create_tile": -1}, "Material"},
		{map[string]any{}, "Material"},
	} {
		require.Equalf(t, c.want, content.ItemKind(statsFrom(c.fields)),
			"%v is filed differently", c.fields)
	}
}

// And every NPC, with the same most-specific-first rule.
func TestNPCKind(t *testing.T) {
	for _, c := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{"boss": true, "damage": 50}, "Boss"},
		{map[string]any{"town": true, "damage": 10}, "Town NPC"},
		{map[string]any{"boss": true, "town": true}, "Boss"},
		// A critter is one that cannot hurt you.
		{map[string]any{"damage": 0}, "Critter"},
		{map[string]any{"damage": 15}, "Monster"},
	} {
		require.Equalf(t, c.want, content.NPCKind(npcStatsFrom(c.fields)),
			"%v is filed differently", c.fields)
	}
}

// A wiki link, including for a name with awkward spacing.
func TestWikiURL(t *testing.T) {
	for name, want := range map[string]string{
		"Copper Pickaxe":    "https://terraria.wiki.gg/wiki/Copper_Pickaxe",
		"Meowmere":          "https://terraria.wiki.gg/wiki/Meowmere",
		"Shield of Cthulhu": "https://terraria.wiki.gg/wiki/Shield_of_Cthulhu",
		"  spaced  out  ":   "https://terraria.wiki.gg/wiki/spaced_out",
	} {
		require.Equalf(t, want, content.WikiURL(name), "%q links elsewhere", name)
	}
}
