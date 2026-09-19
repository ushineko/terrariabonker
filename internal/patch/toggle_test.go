package patch_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
Applying and removing every patch there is.

This is the one package where being wrong costs more than an error message: it
writes instructions into a running process, and a byte in the wrong place does
not fail -- the game runs the wrong code and dies later, somewhere with no
connection to the trainer.

So the bytes are frozen. Each patch is applied to a planted game and the whole
buffer is digested, then removed and digested again. A change to any anchor,
any stub body, any patch site or any displaced byte moves one of these, and has
to be confirmed rather than noticed.

Every digest here was agreed with the implementation this was ported from,
while both existed -- and the anchors themselves were checked against the live
game: all seventeen resolved to the same addresses in both.
*/

// digest is a value as one short string, for freezing it without copying it.
func digest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "unencodable"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

/*
Every patch applies, reads as applied, comes off again, and reads as off.

Two of them do not put the game back byte for byte, and that is deliberate: a
cheat carrying a player field writes its *off* value there rather than restoring
what was found, and a stub's cave is scrubbed to 0xCC rather than to whatever it
held. The `restored` column says which.
*/
func TestEveryPatchGoesOnAndComesOff(t *testing.T) {
	for _, c := range []struct {
		name     string
		on, off  string
		restored bool
	}{
		{"mining", "e843e51d1df4b902", "915ced6dab7f6565", false},
		{"reach", "3d829e93cacfee0d", "bbdc09e2e4ea1b23", false},
		{"pylons", "937f85c3305f7b11", "770bcccf80ab1280", true},
		{"fast_place", "4b7075de42953ddd", "770bcccf80ab1280", true},
		{"tool_reach", "f13a1dc199d5c9ea", "298c8e1655cc251f", false},
		{"smart_cursor", "38ad1c8e633bcf7e", "22e57c0460c1878e", false},
		{"ore_extract", "68a05d59cba34d3b", "ba18fa81c83287f4", false},
		{"max_minions", "e9d9ccfa044630dd", "770bcccf80ab1280", true},
		{"spawn_rate", "ba1d135e4bf3824b", "297a299b6dcca1f8", false},
		{"loot", "ff2dd4a3c5f23370", "9eadf7afdf252fcf", false},
		{"vanity_accs", "e491a546a323b857", "9e7c9656d795c1e2", false},
		{"inventory_accs", "cb0e803383eab95e", "9fade716938ecd6e", false},
		{"pickup", "d379746ad552f997", "c08647478b6eb1f7", false},
		{"auto_use", "90a6307de2d0bf95", "7f95e65c20fcee27", false},
		{"teleport", "fe5616dd3550e72d", "ea3175051e8308d1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			atHome(t)
			mem := plantGame(t)
			before := mem.Hex()
			p := newPatcher(t, mem)

			require.NoError(t, p.Enable(c.name, nil), "applying was refused")
			require.True(t, p.IsEnabled(c.name), "it does not read as applied")
			require.Equal(t, c.on, digest(mem.Hex())[:16],
				"different bytes applied; if that was deliberate, update the digest")

			require.NoError(t, p.Disable(c.name), "removing was refused")
			require.False(t, p.IsEnabled(c.name), "it still reads as applied")
			require.Equal(t, c.off, digest(mem.Hex())[:16],
				"different bytes left behind; if that was deliberate, update the digest")

			require.Equal(t, c.restored, before == mem.Hex(),
				"it put the game back, or did not, differently from what is written here")
		})
	}
}

/*
Re-applying is not applying twice.

The window re-applies on a value change, and the second pass writes over its own
jump: the anchor must not be resolved again, because some injection sites
overlap their own pattern and a pristine scan finds nothing once the jump is in
place.
*/
func TestReapplyingIsIdempotent(t *testing.T) {
	for _, name := range []string{"tool_reach", "pickup", "loot", "mining", "max_minions"} {
		t.Run(name, func(t *testing.T) {
			atHome(t)
			mem := plantGame(t)
			p := newPatcher(t, mem)

			require.NoError(t, p.Enable(name, nil))
			once := mem.Hex()
			require.NoError(t, p.Enable(name, nil), "the second pass was refused")
			require.Equal(t, once, mem.Hex(), "applying it twice wrote something new")

			require.NoError(t, p.Disable(name))
			require.False(t, p.IsEnabled(name), "it survived being removed")
		})
	}
}

/*
The anchor table is frozen whole: bytes, wildcards, uniqueness, verified builds
and the seed each scan searches for.

A byte typed wrong here does not fail. It matches somewhere else -- and the
patch lands in the middle of an unrelated method -- or it matches nothing and
takes a cheat offline, which reads as a game update.
*/
func TestTheAnchorTableIsWhatItWas(t *testing.T) {
	names := make([]string, 0, len(patch.Anchors))
	for name := range patch.Anchors {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Len(t, names, 17, "an anchor has appeared or gone")

	table := map[string]any{}
	for _, name := range names {
		a := patch.Anchors[name]
		off, seed := a.Pattern.Seed()
		require.NotEmptyf(t, seed, "%s has no fixed bytes to search for", name)
		require.NotEmptyf(t, a.Verified, "%s was never verified against a build", name)
		table[name] = map[string]any{
			"raw": a.Pattern.Raw, "mask": a.Pattern.Mask, "unique": a.Unique,
			"verified": a.Verified, "seedOff": off, "seed": seed,
		}
	}
	require.Equal(t,
		"4dc41cc7c6e72969b29d9440cdefc4c78326cf8675d181df4b478d2c69fc850d", digest(table),
		"the anchors have changed; if that was deliberate, update the digest")
}

/*
And the catalog, which is what the window draws its controls from.

Frozen for the labels as much as the names: the order is the order they appear
in, and a section changed here moves a control somewhere nobody expects it.
*/
func TestTheCatalogIsWhatItWas(t *testing.T) {
	catalog := patch.Catalog()
	require.Len(t, catalog, 15, "a patch has appeared or gone")

	rows := []map[string]any{}
	for _, info := range catalog {
		require.NotEmptyf(t, info.Label, "%s has no label", info.Name)
		require.NotEmptyf(t, info.Section, "%s is in no section", info.Name)
		require.Containsf(t, []string{"cheat", "injection"}, info.Kind,
			"%s is neither a cheat nor an injection", info.Name)
		rows = append(rows, map[string]any{"name": info.Name, "label": info.Label,
			"section": info.Section, "kind": info.Kind, "note": info.Note})
	}
	require.Equal(t,
		"6cbb7e77e56e698fe18ca8837c697787c0b54400a1ee3fe56423a87009f2048a", digest(rows),
		"the catalog has changed; if that was deliberate, update the digest")
}
