package content_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

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

// pyPlant is the same objects as Python source, written with the Python's own
// offsets.
func pyPlant(objects []item) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import content, inventory, npcs, recipes
OFF = {}
for mod in (inventory, npcs, recipes):
    for k, v in vars(mod).items():
        if k.isupper() and isinstance(v, int):
            OFF.setdefault(k, v)
mem = FakeMem(%d, %d)
`, base, size)
	for _, o := range objects {
		fmt.Fprintf(&b, "mem.poke_bytes(%d, struct.pack(\"<I\", %d))\n", o.at, vtable)
		for _, name := range sortedFields(o.fields) {
			fmt.Fprintf(&b, "mem.poke_i32(%d + OFF[%q], %d)\n", o.at, name, o.fields[name])
		}
	}
	return b.String()
}

// Every item template is picked out the same way, and the same copy wins.
func TestFindingItemTemplatesMatchesThePython(t *testing.T) {
	var want map[string]any
	askPython(t, pyPlant(itemImage)+fmt.Sprintf(`
got = content.find_item_templates(mem, %d, exclude=(%d,))
print(json.dumps({str(k): v for k, v in got.items()}))`, vtable, excluded), &want)

	got := content.FindItemTemplates(plant(itemImage), vtable, map[uint32]bool{excluded: true})
	require.Len(t, got, len(want), "a different number of templates was found")

	for key, w := range want {
		var typ int32
		_, err := fmt.Sscanf(key, "%d", &typ)
		require.NoError(t, err)
		stats, known := got[typ]
		require.Truef(t, known, "item %s is a template there and not here", key)
		require.Equalf(t, w, asJSON(t, stats), "item %s has different defaults", key)
	}
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

// The NPC templates cluster the same way, and the duplicate drops without taking
// its neighbour with it.
func TestFindingNPCTemplatesMatchesThePython(t *testing.T) {
	var want map[string]any
	askPython(t, pyPlant(npcImage)+fmt.Sprintf(`
got = content.find_npc_templates(mem, %d)
print(json.dumps({str(k): v for k, v in got.items()}))`, vtable), &want)

	got := content.FindNPCTemplates(plant(npcImage), vtable, nil)
	require.Len(t, got, len(want), "a different number of templates was found")
	for key, w := range want {
		var id int32
		_, err := fmt.Sscanf(key, "%d", &id)
		require.NoError(t, err)
		stats, known := got[id]
		require.Truef(t, known, "NPC %s is a template there and not here", key)
		require.Equalf(t, w, asJSON(t, stats), "NPC %s has different defaults", key)
	}

	require.NotContains(t, got, int32(5), "a net id that appears twice in a run was kept")
	require.Contains(t, got, int32(6), "a unique neighbour was thrown away with the duplicate")
	require.Equal(t, int32(11), got[6].Life,
		"a live NPC's difficulty-scaled life was taken for its template")
}

// Every item is filed under the same one-word kind.
func TestItemKindMatchesThePython(t *testing.T) {
	cases := []map[string]any{
		{"accessory": true, "damage": 50},                 // an accessory that hits
		{"head_slot": 98},                                 // armour
		{"head_slot": -1, "body_slot": -1, "leg_slot": 0}, // vanity is still worn
		{"pick": 35, "damage": 4},                         // a pickaxe that hits
		{"damage": 12, "melee": true},
		{"damage": 20, "magic": true},
		{"damage": 15, "ranged": true},
		{"damage": 30, "summon": true, "buff_type": 40}, // a staff, not a potion
		{"heal_life": 100},
		{"buff_type": 11},   // a buff potion heals nothing
		{"defense": 4},      // armour with no slot
		{"create_tile": 0},  // a block
		{"create_tile": -1}, // and a material
		{},
	}
	var want []string
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import content
BASE = {"accessory": False, "melee": False, "ranged": False, "magic": False,
        "summon": False, "head_slot": -1, "body_slot": -1, "leg_slot": -1,
        "create_tile": -1, "damage": 0, "defense": 0, "pick": 0,
        "heal_life": 0, "heal_mana": 0, "buff_type": 0}
print(json.dumps([content.item_kind({**BASE, **c}) for c in %s]))`, pyJSON(cases)), &want)

	for i, c := range cases {
		require.Equalf(t, want[i], content.ItemKind(statsFrom(c)),
			"case %d is filed differently", i)
	}
}

// And every NPC.
func TestNPCKindMatchesThePython(t *testing.T) {
	cases := []map[string]any{
		{"boss": true, "damage": 50},
		{"town": true, "damage": 10},
		{"boss": true, "town": true}, // boss wins
		{"damage": 0},                // a critter is one that cannot hurt you
		{"damage": 15},
	}
	var want []string
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import content
BASE = {"boss": False, "town": False, "damage": 0}
print(json.dumps([content.npc_kind({**BASE, **c}) for c in %s]))`, pyJSON(cases)), &want)

	for i, c := range cases {
		require.Equalf(t, want[i], content.NPCKind(npcStatsFrom(c)),
			"case %d is filed differently", i)
	}
}

// A wiki link is built the same way, including for a name with awkward spacing.
func TestWikiURLMatchesThePython(t *testing.T) {
	names := []string{"Copper Pickaxe", "Meowmere", "Shield of Cthulhu", "  spaced  out  "}
	var want []string
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import content
print(json.dumps([content.wiki_url(n) for n in %s]))`, pyJSON(names)), &want)

	for i, n := range names {
		require.Equalf(t, want[i], content.WikiURL(n), "%q links elsewhere", n)
	}
}
