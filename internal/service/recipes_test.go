package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/recipes"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
The crafting book through the service: the walk itself is internal/recipes' to
test; what is the service's is handing it the right base for the runtime -- Main's
static block under mono, Main.player's slot under the CLR -- and saying so when
there is none.

One recipe is planted each way, from literals: 3 torches (8), by hand.

  - Mono, the numbers internal/layout has always held: Main.recipe 0xA68 into the
    static block; an szarray's length at +0x0C and elements from +0x10;
    Recipe.createItem 0x08, requiredTile 0x1C; Item.type 0x6C, stack 0x88,
    createTile 0xA0.
  - CLR (cmd/clrfields, 2026-10-11): Main.recipe's slot 8 below Main.player's;
    length +0x04, elements +0x08; Recipe.createItem 0x04, requiredItem 0x08,
    requiredTile 0x18; Item.type 0x50, stack 0x64, createTile 0x7C.
*/

var torches = []game.Recipe{{Out: 8, N: 3, Ing: [][]int{{23, 1}, {9, 1}}}}

func TestTheBookUnderTheCLRIsReadFromMainPlayersSlot(t *testing.T) {
	const arr, rec, made, items, gel, wood = csBase + 0x50000, csBase + 0x50100,
		csBase + 0x51000, csBase + 0x52000, csBase + 0x53000, csBase + 0x54000
	w, svc := csGame(t)
	w.Mem.PokeBytes(w.Slot-0x08, u32(arr))
	w.Mem.PokeI32(arr+0x04, 1)
	w.Mem.PokeBytes(arr+0x08, u32(rec))
	w.Mem.PokeBytes(rec+0x04, u32(made))
	w.Mem.PokeBytes(rec+0x08, u32(items))
	w.Mem.PokeI32(rec+0x18, -1)
	w.Mem.PokeI32(made+0x50, 8)
	w.Mem.PokeI32(made+0x64, 3)
	w.Mem.PokeI32(made+0x7C, -1)
	w.Mem.PokeI32(items+0x04, 2)
	for i, ing := range []struct {
		at  uint32
		typ int32
	}{{gel, 23}, {wood, 9}} {
		w.Mem.PokeBytes(items+0x08+uint32(i)*4, u32(ing.at)) //nolint:gosec // an index
		w.Mem.PokeI32(ing.at+0x50, ing.typ)
		w.Mem.PokeI32(ing.at+0x64, 1)
	}

	book, err := svc.Recipes()
	require.NoError(t, err)
	require.Equal(t, torches, book.Recipes)
}

// With no Main.player slot to start from -- nobody in a world -- there is no
// book, and the reason is the statics, not the array.
func TestTheBookUnderTheCLRNeedsTheStatics(t *testing.T) {
	w, svc := csGame(t)
	w.Mem.PokeBytes(w.Slot, u32(0))

	_, err := svc.Recipes()
	var said *service.Error
	require.ErrorAs(t, err, &said)
	require.Contains(t, said.Message, "Main's statics")
}

// Under mono the base is Main's static block, found as every other read finds
// it; a block with no recipe array says so in the walk's own words.
func TestTheBookUnderMonoIsReadFromTheStaticBlock(t *testing.T) {
	const arr, rec, made = base + 0xA0000, base + 0xA0100, base + 0xA1000
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	svc := service.New(mem, -1)

	_, err := svc.Recipes()
	require.ErrorIs(t, err, recipes.ErrNoRecipes)

	mem.PokeBytes(staticAt+0xA68, u32(arr))
	mem.PokeI32(arr+0x0C, 1)
	mem.PokeBytes(arr+0x10, u32(rec))
	mem.PokeBytes(rec+0x08, u32(made))
	mem.PokeI32(rec+0x1C, -1)
	mem.PokeI32(made+0x6C, 8)
	mem.PokeI32(made+0x88, 3)
	mem.PokeI32(made+0xA0, -1)

	book, err := svc.Recipes()
	require.NoError(t, err)
	require.Len(t, book.Recipes, 1)
	require.Equal(t, 8, book.Recipes[0].Out)
	require.Equal(t, 3, book.Recipes[0].N)
}
