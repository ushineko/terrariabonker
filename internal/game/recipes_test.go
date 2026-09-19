package game_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
The crafting book.

The book is a file in data/, written by `extract-recipes` out of a running game,
so the record is the file. What is asserted here is that it is read whole and
read into the right shape -- a recipe decoded wrong is a player told to craft
the wrong thing, and there is no way to notice from the inside because the table
is the answer.

The counts were agreed with the implementation this was ported from.
*/
func TestTheWholeRecipeBookIsRead(t *testing.T) {
	book, err := game.CraftingBook()
	require.NoError(t, err)
	require.Len(t, book.Recipes, 3603, "the book has changed size")
	require.Len(t, book.Stations, 35, "a crafting station has appeared or gone")
	require.Len(t, book.TileIcons, 1923, "a placeable item's tile icon has")

	byHand, atStation := 0, 0
	for i, r := range book.Recipes {
		require.Positivef(t, r.Out, "recipe %d makes nothing", i)
		require.Positivef(t, r.N, "recipe %d makes none of it", i)
		if r.Tile == nil {
			byHand++
		} else {
			atStation++
		}
	}
	require.Positive(t, byHand, "nothing in the book is made by hand")
	require.Positive(t, atStation, "nothing in the book is made at a station")
}

/*
A recipe says where it is made.

A tile the book has no name for is reported as its number rather than as
nothing: a recipe that has to be made somewhere and cannot say where is worse
than one naming a tile the reader can look up.
*/
func TestARecipeSaysWhereItIsMade(t *testing.T) {
	book, err := game.CraftingBook()
	require.NoError(t, err)

	anvil, unknown := 16, 4242
	require.Equal(t, "by hand", book.Station(game.Recipe{}))
	require.Equal(t, "Anvil", book.Station(game.Recipe{Tile: &anvil}),
		"16 is a station the book knows")
	require.Equal(t, "tile 4242", book.Station(game.Recipe{Tile: &unknown}))
}

/*
Both directions of the book, which are the window's two modes.

Makes and Uses are the same recipes seen from opposite ends. Wood is the case
worth pinning: it is made eight ways and used by a hundred and thirty-five, so a
lookup that returned the wrong direction would still return something.
*/
func TestBothDirectionsOfTheBook(t *testing.T) {
	book, err := game.CraftingBook()
	require.NoError(t, err)

	for _, c := range []struct{ item, makes, uses int }{
		{item: 9, makes: 8, uses: 135},  // Wood
		{item: 757, makes: 1, uses: 1},  // Terra Blade
		{item: 3507, makes: 1, uses: 1}, // Copper Shortsword
		{item: 8, makes: 1, uses: 349},  // Torch
	} {
		require.Lenf(t, book.Makes(c.item), c.makes, "item %d is made by a different number", c.item)
		require.Lenf(t, book.Uses(c.item), c.uses, "item %d is used by a different number", c.item)

		for _, r := range book.Makes(c.item) {
			require.Equalf(t, c.item, r.Out, "a recipe that does not make item %d", c.item)
		}
	}
}
