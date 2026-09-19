package gui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
seedCatalog gives a window a small catalog: two bosses and four items, which is
enough for both filters and for a name lookup.

Built by decoding the CLI's own JSON rather than by filling the structs in, so
the fixture cannot drift from the wire format. A hand-filled one did: it carried
only numeric stats, while the game reports `accessory` and the damage classes as
booleans and an NPC's colour as an array, and the tab was empty against every
real game while every test passed.
*/
func seedCatalog(u *ui) {
	const catalog = `{"items":[
		{"id":4,"name":"Eye of Cthulhu Trophy","kind":"Trophy",
		 "stats":{"type":4,"rare":1,"accessory":false,"melee":false,"ranged":false,
		          "magic":false,"summon":false}},
		{"id":9,"name":"Wood","kind":"Material",
		 "stats":{"type":9,"rare":0,"accessory":false,"melee":false,"ranged":false,
		          "magic":false,"summon":false}},
		{"id":757,"name":"Terra Blade","kind":"Sword",
		 "stats":{"type":757,"damage":95,"rare":8,"accessory":false,"melee":true,
		          "ranged":false,"magic":false,"summon":false}},
		{"id":3506,"name":"Copper Pickaxe","kind":"Pickaxe",
		 "stats":{"type":3506,"damage":4,"rare":0,"accessory":false,"melee":true,
		          "ranged":false,"magic":false,"summon":false}}],
	 "npcs":[
		{"id":266,"net_id":266,"name":"Brain of Cthulhu","kind":"Boss",
		 "stats":{"type":266,"net_id":266,"damage":30,"defense":14,"life":1250,
		          "boss":true,"town":false,"color":[0,0,0,0]}},
		{"id":4,"net_id":4,"name":"Eye of Cthulhu","kind":"Boss",
		 "stats":{"type":4,"net_id":4,"damage":15,"defense":12,"life":2800,
		          "boss":true,"town":false,"color":[0,0,0,0]}}]}`

	// The CLI answers on one line; the fixture is wrapped to be readable.
	flat := strings.ReplaceAll(strings.ReplaceAll(catalog, "\n", ""), "\t", "")
	cat, ok := client.ParseCompendium(flat)
	if !ok {
		panic("the seeded catalog does not parse")
	}
	u.takeCatalog(cat)
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
	stats := client.Stats{"damage": float64(0)}
	require.Equal(t, "0", statText(stats, "damage"))
	require.Equal(t, "—", statText(stats, "defense"))
	require.Equal(t, "—", statText(nil, "damage"))
	require.Equal(t, "95", statText(client.Stats{"damage": float64(95)}, "damage"))

	// A flag is a stat too: the catalog reports accessory and the damage
	// classes as booleans, and the colour of an NPC as an array, which is not a
	// number and must read as absent rather than as zero.
	require.Equal(t, "1", statText(client.Stats{"accessory": true}, "accessory"))
	require.Equal(t, "0", statText(client.Stats{"accessory": false}, "accessory"))
	require.Equal(t, "—", statText(client.Stats{"color": []any{float64(0)}}, "color"))
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

	// The list is a dialog now, so the card is not where the names are.
	require.Contains(t, fynetest.Texts(u.sellCard()), "The sell list: 2 items")

	u.sh.Window.Resize(fyne.NewSize(900, 700)) // a dialog lays its list out to fit
	u.showSellList()
	texts := fynetest.Texts(u.sh.Window.Canvas().Overlays().Top())
	require.Contains(t, texts, "Terra Blade (#757)")
	require.Contains(t, texts, "Wood (#9)")
	for _, text := range texts {
		require.NotContainsf(t, text, "item type", "%q is a number where a name should be", text)
	}
}

/*
The card says how much is on the list, so the button is worth reading.

A button that only said "The sell list" would make someone open a dialog to
learn there is nothing in it.
*/
func TestTheSellCardSaysHowMuchIsOnTheList(t *testing.T) {
	require.Equal(t, "The sell list is empty", sellListLabel(0))
	require.Equal(t, "The sell list: 1 item", sellListLabel(1))
	require.Equal(t, "The sell list: 12 items", sellListLabel(12))
}

// Removing with nothing ticked says so rather than doing nothing, which reads
// as a button that is broken. And with no list open there is nothing to remove.
func TestRemovingNothingSaysSo(t *testing.T) {
	u := testUI(t)
	u.fx.whitelist = []int{757}
	require.NotPanics(t, func() { u.removeFromSellList() }, "no list has been opened")

	u.sh.Window.Resize(fyne.NewSize(900, 700))
	u.showSellList()
	require.NotPanics(t, func() { u.removeFromSellList() }, "the list is open and nothing is ticked")
	require.Equal(t, []int{757}, u.fx.whitelist, "and nothing was taken off")
}

/*
Several items can be ticked and removed in one go.

The list is edited in bursts -- whitelist a few things, change your mind about
several -- and taking them off one at a time meant a reload between each, with
the list jumping under the pointer.

This drives the ticks the dialog actually drew, which is also the check that a
PickList's rows reach the screen at all: the widget's own tests build rows
directly, because a list draws none until something lays it out.
*/
func TestSeveralSellListItemsCanBeRemovedAtOnce(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	t.Cleanup(u.shutdown)
	u.iv.names = map[int]string{757: "Terra Blade", 9: "Wood", 29: "Life Crystal"}
	u.fx.whitelist = []int{757, 9, 29}

	u.sh.Window.Resize(fyne.NewSize(900, 700))
	u.showSellList()
	overlay := u.sh.Window.Canvas().Overlays().Top()
	require.NotNil(t, overlay)

	boxes := fynetest.All[*widget.Check](overlay)
	require.Len(t, boxes, 3, "a tick per row, drawn by the dialog")
	require.Contains(t, fynetest.Texts(overlay), "Remove", "and nothing to remove yet")

	boxes[0].SetChecked(true)
	boxes[2].SetChecked(true)
	require.Equal(t, []int{0, 2}, u.fx.sellList.Picked())
	require.Contains(t, fynetest.Texts(overlay), "Remove 2 items",
		"the button says what it is about to do")

	// Removing sends one command per item and reads the list back once.
	u.removeFromSellList()
	require.Empty(t, u.fx.sellList.Picked(), "the picks are row numbers, and the rows changed")
}

/*
TestTheCatalogDrawsOnlyAScreenfulsWorth: the tab is a search over 6,954 entries,
and Fyne's SetRowHeight refreshes the whole table on every call, so a row costs
something to build whether or not anyone looks at it. Drawing them all took
gigabytes and stopped the window answering.
*/
func TestTheCatalogDrawsOnlyAScreenfulsWorth(t *testing.T) {
	u := testUI(t)
	rows := make([]catalogEntry, 0, maxRows*3)
	for i := range maxRows * 3 {
		rows = append(rows, catalogEntry{
			ID: i, Name: fmt.Sprintf("Item %d", i), Kind: "Sword",
			Stats: client.Stats{"damage": float64(i), "accessory": false},
		})
	}
	u.cp.entries = rows

	table, ok := u.compendiumTable(rows).(*widget.Table)
	require.True(t, ok, "the catalog is drawn as a table")
	drawn, _ := table.Length()
	require.Equal(t, maxRows, drawn, "every match was drawn, not just a screenful")

	// And the count says so, rather than leaving the reader to wonder where the
	// other matches went.
	require.Equal(t, "first 300 of 900 matches (of 900) -- narrow the filter",
		countText(len(rows), len(u.cp.entries)))
	require.Equal(t, "12 of 900 entries match", countText(12, len(u.cp.entries)))
}
