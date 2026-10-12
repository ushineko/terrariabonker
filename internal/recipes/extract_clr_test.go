package recipes_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/proc"
	"github.com/ushineko/terrariabonker/internal/recipes"
)

/*
The same book, in a game laid out as .NET Framework lays it out (spec 052).

Every number here is a literal from the measurement, not the entry's: cmd/clrfields
against the live game, 2026-10-11 (docs/clr-fields-1.4.5.8.txt). An szarray keeps
its length at +0x04 and its elements from +0x08. Recipe: createItem 0x04,
requiredItem 0x08, requiredTile 0x18. Item: type 0x50, stack 0x64, createTile
0x7C, placeStyle 0x84. LocalizedText._value 0x08.

The three statics are slots at distances from Main.player's: Main.recipe 8 below
(0x870 against 0x878), MapHelper.tileLookup 0x85C above (0x10D4) and
Lang._mapLegendCache 0x7BC below (0x0BC). There is no code to find them through,
so none is planted.
*/
const (
	clrPlayerSlot = base + 0x10000
	clrRecipeSlot = clrPlayerSlot - 0x08
	clrLookupSlot = clrPlayerSlot + 0x85C
	clrLegendSlot = clrPlayerSlot - 0x7BC

	clrArrLen, clrArrData                           = 0x04, 0x08
	clrCreateItem, clrRequiredItem, clrRequiredTile = 0x04, 0x08, 0x18
	clrItemType, clrItemStack                       = 0x50, 0x64
	clrCreateTile, clrPlaceStyle                    = 0x7C, 0x84
	clrTextValue                                    = 0x08
)

func clrEntry(t *testing.T) layout.Entry {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return e
}

// plantCLR is the fixture's book as the CLR would hold it.
func plantCLR(t *testing.T) *execMem {
	t.Helper()
	mem := memtest.New(base, size)
	mem.Exec = []proc.Region{{Start: base, End: base + size, Executable: true}}

	mem.PokeBytes(clrRecipeSlot, u32(recipeArr))
	mem.PokeI32(recipeArr+clrArrLen, int32(len(book))) //nolint:gosec // a small count
	for i, rec := range book {
		obj := uint32(recipeObjects + i*recipeStride) //nolint:gosec // a planted address
		mem.PokeBytes(recipeArr+clrArrData+uint32(i)*4, u32(obj))
		if rec.out == 0 && !rec.planted {
			continue
		}
		made := uint32(itemObjects + i*itemStride) //nolint:gosec // a planted address
		mem.PokeBytes(obj+clrCreateItem, u32(made))
		mem.PokeI32(made+clrItemType, rec.out)
		mem.PokeI32(made+clrItemStack, rec.stack)
		mem.PokeI32(made+clrCreateTile, rec.createTile)
		mem.PokeI32(made+clrPlaceStyle, rec.placeStyle)
		mem.PokeI32(obj+clrRequiredTile, rec.tile)

		items := uint32(itemArrays + i*itemStride) //nolint:gosec // a planted address
		mem.PokeBytes(obj+clrRequiredItem, u32(items))
		mem.PokeI32(items+clrArrLen, int32(len(rec.ing))) //nolint:gosec // a small count
		for j, pair := range rec.ing {
			ing := ingredientAt(i, j)
			mem.PokeBytes(items+clrArrData+uint32(j)*4, u32(ing)) //nolint:gosec // an index
			mem.PokeI32(ing+clrItemType, pair[0])
			mem.PokeI32(ing+clrItemStack, pair[1])
		}
	}

	mem.PokeBytes(clrLookupSlot, u32(lookupArr))
	mem.PokeBytes(clrLegendSlot, u32(legendArr))
	mem.PokeI32(lookupArr+clrArrLen, 700)
	mem.PokeBytes(lookupArr+clrArrData+benchTile*2, []byte{benchLegend, 0})
	mem.PokeI32(legendArr+clrArrLen, 64)
	mem.PokeBytes(legendArr+clrArrData+benchLegend*4, u32(legendText))
	mem.PokeBytes(legendText+clrTextValue, u32(stationNameAt))
	mem.PlantCLRString(stationNameAt, benchName)
	return &execMem{mem}
}

// Under the CLR the walk reads the same book, from Main.player's slot.
func TestTheBookUnderTheCLRIsWhatWasPlanted(t *testing.T) {
	got, err := recipes.Extract(plantCLR(t), clrEntry(t), clrPlayerSlot)
	require.NoError(t, err)
	requireTheBook(t, got)
}

/*
Each runtime's numbers find no book in the other's game.

A book read with the wrong layout is the failure this guards: it would be written
into the checkout and browsed for months. Mono's numbers on the CLR image look
for Main.recipe 0xA68 into a block that is not there; the CLR's on the mono image
look 8 below the static block.
*/
func TestOneRuntimesNumbersFindNoBookInTheOthers(t *testing.T) {
	_, err := recipes.Extract(plantCLR(t), layout.Mono(), clrPlayerSlot)
	require.ErrorIs(t, err, recipes.ErrNoRecipes)

	_, err = recipes.Extract(plant(t), clrEntry(t), staticAt)
	require.ErrorIs(t, err, recipes.ErrNoRecipes)
}

// A slot holding no array, or one of absurd length, is no book under the CLR
// either.
func TestExtractingUnderTheCLRWithNoArray(t *testing.T) {
	mem := plantCLR(t)
	mem.PokeI32(recipeArr+clrArrLen, recipes.MaxRecipes+1)
	_, err := recipes.Extract(mem, clrEntry(t), clrPlayerSlot)
	require.ErrorIs(t, err, recipes.ErrNoRecipes)

	mem.PokeBytes(clrRecipeSlot, u32(0))
	_, err = recipes.Extract(mem, clrEntry(t), clrPlayerSlot)
	require.ErrorIs(t, err, recipes.ErrNoRecipes)
}

/*
With either tile-name static empty the book is still read, its stations unnamed.

The two are other classes' statics, null until the game first draws its map
legend; a book with "Tile #N" for a station is still a book.
*/
func TestUnderTheCLRAnEmptyNameStaticLeavesStationsUnnamed(t *testing.T) {
	for name, slot := range map[string]uint32{"tileLookup": clrLookupSlot, "_mapLegendCache": clrLegendSlot} {
		mem := plantCLR(t)
		mem.PokeBytes(slot, u32(0))
		got, err := recipes.Extract(mem, clrEntry(t), clrPlayerSlot)
		require.NoError(t, err, name)
		require.Len(t, got.Recipes, 4, name)
		require.Empty(t, got.Stations, "a station was named without %s", name)
	}
}

// An entry naming no way to find the statics reads nothing and says which entry.
func TestAnEntryWithNoRecipeResolverIsRefused(t *testing.T) {
	e := layout.Derive(clrEntry(t), func(e *layout.Entry) {
		e.Name, e.Recipes.Resolver = "some-runtime", ""
	})
	_, err := recipes.Extract(plantCLR(t), e, clrPlayerSlot)
	require.ErrorContains(t, err, "some-runtime")
}
