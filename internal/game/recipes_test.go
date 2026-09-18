package game_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
The whole recipe book is the book the Python reads.

Every one of 3,603 recipes, field for field: what it makes, how many, what it
takes and where. A recipe read wrong is a player told to craft the wrong thing,
and there is no way to notice from the inside -- the table is the answer.
*/
func TestTheWholeRecipeBookMatchesThePython(t *testing.T) {
	var want struct {
		Recipes []struct {
			Out  int     `json:"out"`
			N    int     `json:"n"`
			Ing  [][]int `json:"ing"`
			Tile *int    `json:"tile"`
		} `json:"recipes"`
		Stations  map[string]string `json:"stations"`
		TileIcons map[string][]int  `json:"tileicons"`
	}
	askPython(t, `
import json
from terrariabonker import recipes as rec
print(json.dumps(rec.load()))
`, &want)

	book, err := game.CraftingBook()
	require.NoError(t, err)
	require.Len(t, book.Recipes, len(want.Recipes))
	require.Greater(t, len(book.Recipes), 3000, "and neither book is empty")

	for i, r := range want.Recipes {
		got := book.Recipes[i]
		require.Equalf(t, r.Out, got.Out, "recipe %d makes something else", i)
		require.Equalf(t, r.N, got.N, "recipe %d makes a different number", i)
		require.Equalf(t, r.Ing, got.Ing, "recipe %d takes something else", i)
		require.Equalf(t, r.Tile, got.Tile, "recipe %d is made somewhere else", i)
	}
	require.Equal(t, want.Stations, book.Stations)
	require.Equal(t, want.TileIcons, book.TileIcons)
}

/*
A recipe says where it is made.

The station names come from the book; a tile it has no name for is reported as
its number rather than as nothing, because a recipe that has to be made
somewhere and cannot say where is worse than one naming a tile to look up.
*/
func TestARecipeSaysWhereItIsMade(t *testing.T) {
	book, err := game.CraftingBook()
	require.NoError(t, err)

	anvil, unknown := 16, 4242
	require.Equal(t, "by hand", book.Station(game.Recipe{}))
	require.NotEmpty(t, book.Station(game.Recipe{Tile: &anvil}))
	require.NotEqual(t, "tile 16", book.Station(game.Recipe{Tile: &anvil}),
		"16 is a station the book knows")
	require.Equal(t, "tile 4242", book.Station(game.Recipe{Tile: &unknown}))
}

/*
Both directions of the book agree with the Python's.

Makes and Uses are the same recipes seen from opposite ends, and the window's
two modes are built on them.
*/
func TestBothDirectionsMatchThePython(t *testing.T) {
	book, err := game.CraftingBook()
	require.NoError(t, err)

	for _, item := range []int{9, 757, 3507, 8} {
		var want struct {
			Makes int `json:"makes"`
			Uses  int `json:"uses"`
		}
		askPython(t, `
import json
from terrariabonker import recipes as rec
book = rec.load()["recipes"]
item = `+itoa(item)+`
print(json.dumps({
  "makes": sum(1 for r in book if r["out"] == item),
  "uses": sum(1 for r in book if any(t == item for t, _ in r["ing"])),
}))
`, &want)
		require.Lenf(t, book.Makes(item), want.Makes, "item %d is made by a different number of recipes", item)
		require.Lenf(t, book.Uses(item), want.Uses, "item %d is used by a different number", item)
	}
}
