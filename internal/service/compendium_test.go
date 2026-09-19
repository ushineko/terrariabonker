package service_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
The catalog, and the cache that keeps it from costing a scan every time.

Every entry is compared with the Python's, all six thousand of them: the parts
that come from the bundled tables are the easy half, and the parts that come out
of the game's own memory are the half where a missing field looks like an item
the game does not have.
*/

/*
pyCacheAt points the Python's caches at a scratch directory without losing it
its own packages.

HOME cannot simply be set on the child: its interpreter works out where the
user's packages are from HOME at startup, and numpy then goes missing -- which
this package's helper reports as "not importable" and skips, which is worse than
failing. So numpy is loaded first and HOME moved afterwards, which the cache path
picks up because it is built when the cache is written rather than at import.
*/
func pyCacheAt(home string) string {
	return fmt.Sprintf(`
import numpy  # loaded while HOME still finds the user's own packages
import os
os.environ["HOME"] = %q
`, home)
}

// catalogFixture is a game with templates, a world and NPCs in it.
func catalogFixture(t *testing.T) (*execMem, *service.Service) {
	t.Helper()
	mem := plant()
	plantTemplateInto(mem)
	plantWorldInto(mem, "Nakama's World")
	plantNPCsInto(mem)
	return mem, service.New(mem, -1)
}

/*
The entries the game had a template for carry its stats, and the rest say so.

Read from the fixture rather than from the other implementation: two catalogs
that both dropped the stats agree perfectly, and a catalog of names is what this
already had before the templates were read.
*/
func TestTheCatalogCarriesWhatTheGameKnows(t *testing.T) {
	atHome(t)
	_, svc := catalogFixture(t)
	got, err := svc.Compendium(false)
	require.NoError(t, err)
	require.NotEmpty(t, got.Items, "the catalog has no items in it")
	require.NotEmpty(t, got.NPCs, "the catalog has no NPCs in it")

	item := findItem(t, got, templateType)
	require.Equal(t, "Terra Blade", item.Name, "the bundled names are not being used")
	stats := statsOf(t, item.Stats)
	require.EqualValues(t, 40, stats["damage"], "the template's damage did not come across")
	require.Equal(t, "Weapon", item.Kind, "the kind was not worked out from the stats")
	require.Contains(t, item.Wiki, "Terra_Blade", "no wiki link")

	slime := findNPC(t, got, 1)
	require.Equal(t, "Blue Slime", slime.Name)
	require.EqualValues(t, 25, statsOf(t, slime.Stats)["life"],
		"the live slime's scaled life was published as the type's")

	// And one the fixture planted no template for is still listed, without stats.
	bare := findItem(t, got, 1)
	require.Equal(t, "Unknown", bare.Kind, "an item with no template was given a kind")
	require.Empty(t, statsOf(t, bare.Stats), "an item with no template was given stats")
}

// findItem and findNPC are one entry out of the catalog, by id.
func findItem(t *testing.T, c service.Catalog, id int) service.CatalogItem {
	t.Helper()
	for _, e := range c.Items {
		if e.ID == id {
			return e
		}
	}
	require.FailNowf(t, "not in the catalog", "item %d", id)
	return service.CatalogItem{}
}

func findNPC(t *testing.T, c service.Catalog, id int) service.CatalogNPC {
	t.Helper()
	for _, e := range c.NPCs {
		if e.ID == id {
			return e
		}
	}
	require.FailNowf(t, "not in the catalog", "NPC %d", id)
	return service.CatalogNPC{}
}

// statsOf is an entry's stats as a plain map, whatever shape they arrived in.
func statsOf(t *testing.T, stats any) map[string]any {
	t.Helper()
	blob, err := json.Marshal(stats)
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(blob, &out))
	return out
}

// The catalog is in id order, so it does not shuffle between calls.
func TestTheCatalogIsInOrder(t *testing.T) {
	atHome(t)
	_, svc := catalogFixture(t)
	got, err := svc.Compendium(false)
	require.NoError(t, err)

	ids := make([]int, len(got.Items))
	for i, e := range got.Items {
		ids[i] = e.ID
	}
	require.True(t, sort.IntsAreSorted(ids), "the items came back out of order")
}

/*
The second call reads the cache rather than scanning again.

The scan is the whole cost of the catalog, so this is not a nicety: the window
asks for it while the user waits.
*/
func TestTheCatalogComesBackFromTheCache(t *testing.T) {
	home := atHome(t)
	mem, svc := catalogFixture(t)
	first, err := svc.Compendium(false)
	require.NoError(t, err)

	files := cacheFiles(t, home)
	require.Equal(t, []string{"npcs-" + cacheKey(svc) + ".json",
		"templates-" + cacheKey(svc) + ".json"}, files,
		"the two catalogs did not land in their own files")

	// The templates go, so anything that rescans comes back empty-handed.
	mem.PokeI32(templateAt+uint32(layoutItemType), 0)
	mem.PokeBytes(npcTemplateAt, u32(0))

	again, err := service.New(mem, -1).Compendium(false)
	require.NoError(t, err)
	require.Equal(t, findItem(t, first, templateType).Stats,
		findItem(t, again, templateType).Stats, "the catalog was scanned again")
}

// And `refresh` is what gets past it.
func TestRefreshingRescans(t *testing.T) {
	atHome(t)
	mem, svc := catalogFixture(t)
	_, err := svc.Compendium(false)
	require.NoError(t, err)

	mem.PokeI32(templateAt+uint32(layoutItemType), 0)
	again, err := service.New(mem, -1).Compendium(true)
	require.NoError(t, err)
	require.Empty(t, statsOf(t, findItem(t, again, templateType).Stats),
		"the cache was used although a refresh was asked for")
}

/*
A cache written before a field was read is rescanned rather than believed.

Without the three use-time fields an edit cannot be told from an item's own
defaults, so a cache that predates them is not a smaller answer -- it is a wrong
one.
*/
func TestACacheMissingAFieldIsRescanned(t *testing.T) {
	home := atHome(t)
	_, svc := catalogFixture(t)
	first, err := svc.Compendium(false)
	require.NoError(t, err)

	path := filepath.Join(home, ".cache", "terrariabonker",
		"templates-"+cacheKey(svc)+".json")
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"757": {"type": 757, "damage": 1}}`), 0o600))

	again, err := svc.Compendium(false)
	require.NoError(t, err)
	require.Equal(t, findItem(t, first, templateType).Stats,
		findItem(t, again, templateType).Stats,
		"a cache with no use_anim in it was used anyway")
}

// cacheKey is the build key as it appears in a cache filename.
func cacheKey(svc *service.Service) string {
	out := []rune(svc.BuildKey())
	for i, c := range out {
		if c == '+' {
			out[i] = '-'
		}
	}
	return string(out)
}

// cacheFiles is what is in the cache directory, in order.
func cacheFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, ".cache", "terrariabonker"))
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

/*
Building the catalog publishes what the icon extractor cannot read for itself.

The extractor runs unprivileged, so the frame counts and the tints have to be
handed over by the side that can see them -- and the catalog scan is the one
moment they are all in hand.
*/
func TestTheCatalogPublishesTheDrawData(t *testing.T) {
	atHome(t)
	mem, svc := catalogFixture(t)
	plantFrameCountsInto(mem)

	_, err := svc.Compendium(false)
	require.NoError(t, err)

	frames, tints := sprites.LoadNPCDrawData()
	require.NotEmpty(t, frames, "no frame counts were published")
	require.Equal(t, int32(2), frames[1], "a different frame count for the Blue Slime")
	require.Equal(t, sprites.NPCTint{Type: 1, Color: npcSlimeTint}, tints[1],
		"the slime's tint was not published")
	require.NotContains(t, tints, int32(4),
		"an NPC the game does not tint was given a tint anyway")
	/*
		And a variant is filed under its netID while naming the *type* whose
		sheet it paints. For the Blue Slime the two are the same number, which
		is why this asks about the one where they are not.
	*/
	require.Equal(t, sprites.NPCTint{Type: 1, Color: [4]byte{102, 204, 106, 255}},
		tints[-3], "the variant does not say which sheet it tints")
}

/*
Everything written under the user's home is handed back to them.

The scan runs privileged and `sudo -E` keeps HOME, so the cache lands in their
own directory owned by root -- where they cannot clear it, and where the
directory around it stays user-writable. The handing back cannot be exercised
without being root, so what is checked is that it is asked for, on every path
this program created.
*/
func TestTheCachesAreHandedBackToTheUser(t *testing.T) {
	home := atHome(t)
	handed := service.WatchGiveBack(t)
	mem, svc := catalogFixture(t)
	plantFrameCountsInto(mem)

	_, err := svc.Compendium(false)
	require.NoError(t, err)

	dir := filepath.Join(home, ".cache", "terrariabonker")
	require.Equal(t, []string{
		dir, filepath.Join(dir, "templates-"+cacheKey(svc)+".json"),
		dir, filepath.Join(dir, "npcs-"+cacheKey(svc)+".json"),
		sprites.NPCFramesFile(),
	}, *handed, "something written as root was left owned by root")
}
