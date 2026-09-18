package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// seedCatalog gives a window a small catalog: two bosses and four items, which
// is enough for both filters and for a name lookup.
func seedCatalog(u *ui) {
	u.takeCatalog(&client.Compendium{
		Items: []client.Item{
			{ID: 4, Name: "Eye of Cthulhu Trophy", Kind: "Trophy", Stats: map[string]float64{"rare": 1}},
			{ID: 9, Name: "Wood", Kind: "Material", Stats: map[string]float64{"rare": 0}},
			{ID: 757, Name: "Terra Blade", Kind: "Sword", Stats: map[string]float64{"damage": 95, "rare": 8}},
			{ID: 3506, Name: "Copper Pickaxe", Kind: "Pickaxe", Stats: map[string]float64{"damage": 4, "rare": 0}},
		},
		NPCs: []client.NPC{
			{ID: 266, NetID: 266, Name: "Brain of Cthulhu", Kind: "Boss",
				Stats: map[string]float64{"damage": 30, "defense": 14, "life": 1250}},
			{ID: 4, NetID: 4, Name: "Eye of Cthulhu", Kind: "Boss",
				Stats: map[string]float64{"damage": 15, "defense": 12, "life": 2800}},
		},
	})
}

// seedRecipes gives a window a three-recipe book, one of which needs a station.
func seedRecipes(u *ui) {
	anvil := 16
	u.rc.book = &game.Recipes{
		Recipes: []game.Recipe{
			{Out: 757, N: 1, Ing: [][]int{{9, 12}, {4, 1}}, Tile: &anvil},
			{Out: 3506, N: 1, Ing: [][]int{{9, 10}}},
			{Out: 9, N: 4, Ing: [][]int{{4, 1}}},
		},
		Stations: map[string]string{"16": "Anvil"},
	}
	// The indexes too, the way loadRecipes replaces them: the window reads the
	// real book when it is built now, so a fixture that only replaced the book
	// would be indexed as the 3,603-recipe one.
	u.rc.makes, u.rc.uses = nil, nil
	u.indexRecipes()
}

/*
The catalog is narrowed by the kind picker and the filter together.

Matching the id as well as the name is the point of the second half: a player
reading a wiki page knows an item by its number, and typing it must find it.
*/
func TestTheCatalogIsNarrowedByKindAndByText(t *testing.T) {
	u := testUI(t)
	seedCatalog(u)

	require.Len(t, u.visibleEntries(), 6, "Everything is no filter at all")
	require.Equal(t, []string{"Boss", "Material", "Pickaxe", "Sword", "Trophy"}, u.cp.kinds)

	u.cp.kind = "Boss"
	require.Len(t, u.visibleEntries(), 2)

	u.cp.kind, u.cp.filter = anyKind, "cthulhu"
	require.Len(t, u.visibleEntries(), 3, "two NPCs and a trophy")

	u.cp.filter = "3506"
	got := u.visibleEntries()
	require.Len(t, got, 1, "an id someone typed must find its item")
	require.Equal(t, "Copper Pickaxe", got[0].Name)

	// The two catalogs share numbers: item 4 is a trophy and NPC 4 is the boss
	// it came off. Both must come back, because the window cannot tell which
	// one was meant.
	u.cp.filter = "4"
	require.Len(t, u.visibleEntries(), 2)
}

// A stat of zero and a stat the game never reported are different facts: a
// pickaxe that does no damage is not a pickaxe whose damage nobody read.
func TestAnUnreadStatIsNotAZero(t *testing.T) {
	stats := map[string]float64{"damage": 0}
	require.Equal(t, "0", statText(stats, "damage"))
	require.Equal(t, "—", statText(stats, "defense"))
	require.Equal(t, "—", statText(nil, "damage"))
	require.Equal(t, "95", statText(map[string]float64{"damage": 95}, "damage"))
}

/*
The book is indexed both ways from one read.

Makes and Uses are the same recipes seen from opposite ends, and the grid is
filtered on every keystroke: walking 3,600 recipes per character is work that
only has to be done once.
*/
func TestTheRecipeBookIsIndexedBothWays(t *testing.T) {
	u := testUI(t)
	seedCatalog(u)
	seedRecipes(u)

	require.Equal(t, []int{3506, 757, 9}, u.rc.ids, "Makes lists what can be crafted, by name")
	require.Len(t, u.rc.makes[757], 1)

	u.rc.mode = modeUses
	u.indexRecipes()
	require.Equal(t, []int{4, 9}, u.rc.ids, "Uses lists ingredients, by name")
	require.Len(t, u.rc.uses[9], 2, "wood goes into a blade and a pickaxe")

	u.rc.filter = "wood"
	require.Equal(t, []int{9}, u.visibleRecipeItems())
	u.rc.filter = "4"
	require.Equal(t, []int{4}, u.visibleRecipeItems(), "an ingredient found by its id")
}

/*
Both browsing sections must render to an image.

Rendering headlessly is how a layout is checked without a display or anybody to
look at it. Each one is asked for the icon cache as well, because a grid cell
and a table row are both sized around an icon and an empty cache hides that.
*/
func TestTheBrowsingSectionsRenderToAnImage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*ui) fyne.CanvasObject
		texts []string
	}{
		{"compendium", (*ui).buildCompendium, []string{"Brain of Cthulhu", "Boss", "1250", "3506"}},
		{"recipes", (*ui).buildRecipes, []string{"Cop.Pic", "Ter.Bla", "Wood"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := testUI(t)
			seedCatalog(u)
			seedRecipes(u)
			seedSprites(t, u, 9, 757, 3506)

			win := test.NewWindow(tc.build(u))
			defer win.Close()
			win.Resize(fyne.NewSize(1100, 620))

			require.NotPanics(t, func() { _ = win.Canvas().Capture() })
			texts := fynetest.Texts(win.Canvas().Content())
			for _, want := range tc.texts {
				require.Containsf(t, texts, want, "%q is missing from the section", want)
			}
		})
	}
}

/*
seedSprites writes an icon cache the window can read.

The cache is a directory of PNGs the Python extractor wrote, so the test writes
PNGs rather than mocking the reader: the path convention and the file naming are
the contract between the two halves, and a mock would not hold them.
*/
func seedSprites(t *testing.T, u *ui, items ...int) {
	t.Helper()
	u.sprites = newSprites()
	require.NoError(t, os.MkdirAll(u.sprites.dir, 0o750))
	for _, id := range items {
		name := "Item_" + strconv.Itoa(id) + ".png"
		path := filepath.Join(u.sprites.dir, name)
		require.NoError(t, os.WriteFile(path, onePixelPNG(t), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(u.sprites.dir, ".done"), nil, 0o600))
	require.True(t, u.sprites.ready())
}

// onePixelPNG is the smallest thing that decodes as an item icon.
func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 200, G: 60, B: 60, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

/*
A recipe names every ingredient it takes.

Zenith takes ten swords, and the dialog used to put them in one sentence, which
ran off its edge: "Terra Blade x1, Meowmere x1, Star Wrath x1, Influx Waver
x...". A recipe that cannot say what it is made of is the one thing a recipe
book must not do.

Read from the bundled book rather than a fixture, because the recipe that broke
it is a real one and a fixture of two ingredients would never have caught it.
*/
func TestARecipeNamesEveryIngredient(t *testing.T) {
	u := testUI(t)

	zenith := 0
	for id, name := range u.iv.names {
		if name == "Zenith" {
			zenith = id
			break
		}
	}
	require.NotZero(t, zenith, "the bundled names have no Zenith in them")

	made := u.rc.book.Makes(zenith)
	require.NotEmpty(t, made, "the bundled book has no recipe for Zenith")
	recipe := made[0]
	require.Greater(t, len(recipe.Ing), 5, "and it is the many-ingredient one")

	texts := fynetest.Texts(u.recipeCard(recipe))
	require.Contains(t, texts, "Zenith x1", "what it makes")
	require.Contains(t, texts, "at "+u.rc.book.Station(recipe), "and where")
	for _, ing := range recipe.Ing {
		require.Lenf(t, ing, 2, "an ingredient is an item and a count")
		want := u.itemName(ing[0]) + " x" + itoa(ing[1])
		require.Containsf(t, texts, want, "%q is not in the card", want)
	}
}

// itoa is a count as it is written in a row.
func itoa(n int) string { return strconv.Itoa(n) }

/*
The sell list names its items.

It said "item type 3507", which is the number the game uses and not a thing
anybody recognises. It said that because the names came from a subprocess that
had not answered when the section was built; they are read in this process now,
so a row can say what it is.
*/
func TestTheSellListNamesItsItems(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	t.Cleanup(u.shutdown)
	u.iv.names = map[int]string{757: "Terra Blade", 9: "Wood"}
	u.fx.whitelist = []int{757, 9}

	win := test.NewWindow(u.sellCard())
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(500, 400))

	texts := fynetest.Texts(win.Canvas().Content())
	require.Contains(t, texts, "Terra Blade (#757)")
	require.Contains(t, texts, "Wood (#9)")
	for _, text := range texts {
		require.NotContainsf(t, text, "item type", "%q is a number where a name should be", text)
	}
}
