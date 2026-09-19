/*
Package recipes reads the crafting book out of the running game.

The book itself is bundled and read by the game package, which needs no game and
no privilege. This is the other half: walking Main.recipe[] once to produce that
file. It is a maintenance tool rather than something the trainer uses, and it
lives apart for that reason -- browsing recipes must not depend on being able to
read another process's memory.

Station names come from Terraria's own tile display names rather than from a
table written here, so they are exact and build-specific. Finding them needs two
statics that nothing else in the project reaches, so they are resolved from the
code that reads them.

Ported from terrariabonker/recipes.py (spec 051, step 6).
*/
package recipes

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

// Mem is the running game this reads out of.
type Mem interface {
	locate.ExecMem
	ReadU32(addr uint32) (uint32, bool)
	ReadI32(addr uint32) (int32, bool)
}

/*
The two statics the tile-name lookup needs, found by the code that reads them.

MapHelper.tileLookup maps a tile type to a map-legend index, and
Lang._mapLegendCache holds the names that index into. Neither has a fixed
address, and neither is reachable from anything else the project resolves -- so
each is read out of the operand of the instruction that loads it.
*/
var (
	tileLookupPattern = patch.MustParse(
		"8B 05 ?? ?? ?? ?? 39 70 0C 0F 86 ?? ?? ?? ?? " +
			"8D 44 70 10 0F B7 00 03 C7")
	mapLegendPattern = patch.MustParse(
		"8B 05 ?? ?? ?? ?? 85 C0 74 24 8B 05 ?? ?? ?? ?? " +
			"8B 4D 08 39 48 0C 0F 86 ?? ?? ?? ?? 8D 44 88 10 8B 00")
)

// MaxRecipes bounds a plausible Main.recipe array: a wrong pointer read as one
// gives an absurd length.
const MaxRecipes = 50000

// MaxIngredients is how far into a recipe's item array to look. The game's own
// arrays are longer than any recipe uses.
const MaxIngredients = 40

// LocalizedTextValue is where a localised string keeps the text itself, which is
// how a station's name is read rather than its key.
const LocalizedTextValue = layout.LocalizedTextValue

// ErrNoRecipes is what an extraction reports when the array is not there.
var ErrNoRecipes = errors.New("cannot find the Main.recipe array, or it is implausible")

/*
Extract walks Main.recipe[] and is the whole book.

Every recipe the game has, the stations they are made at, and the tile each
placeable output draws from -- that last one so a sprite-less item, like a
trapped chest with no icon file of its own, can still be drawn.
*/
func Extract(mem Mem) (game.Recipes, error) {
	base, ok := locate.MainStaticBase(mem)
	if !ok {
		return game.Recipes{}, errors.New(
			"could not locate Main (the get_LocalPlayer pattern is missing?)")
	}
	arr, ok := readU32(mem, base+uint32(layout.MainRecipeOff)) //nolint:gosec // a field offset
	if !ok || arr == 0 {
		return game.Recipes{}, ErrNoRecipes
	}
	count, ok := readI32(mem, arr+layout.ArrLenOff)
	if !ok || count <= 0 || count > MaxRecipes {
		return game.Recipes{}, ErrNoRecipes
	}

	book := game.Recipes{
		Recipes:   []game.Recipe{},
		Stations:  map[string]string{},
		TileIcons: map[string][]int{},
	}
	var tiles []int32
	seenTile := map[int32]bool{}
	for i := range int(count) {
		obj, ok := readU32(mem, arr+layout.ArrDataOff+uint32(i)*4) //nolint:gosec // an index
		if !ok || obj == 0 {
			continue
		}
		made, ok := readU32(mem, obj+uint32(layout.RecipeCreateItem)) //nolint:gosec // a field offset
		if !ok || made == 0 {
			continue
		}
		out, ok := readI32(mem, made+uint32(layout.ItemType)) //nolint:gosec // a field offset
		if !ok || out == 0 {
			continue // an empty recipe slot
		}
		stack, _ := readI32(mem, made+uint32(layout.ItemStack)) //nolint:gosec // a field offset
		if stack == 0 {
			stack = 1
		}
		items, _ := readU32(mem, obj+uint32(layout.RecipeRequiredItem)) //nolint:gosec // a field offset
		rec := game.Recipe{Out: int(out), N: int(stack), Ing: ingredients(mem, items)}

		if tile, ok := readI32(mem, obj+uint32(layout.RecipeRequiredTile)); ok && tile >= 0 { //nolint:gosec // a field offset
			at := int(tile)
			rec.Tile = &at
			if !seenTile[tile] {
				seenTile[tile] = true
				tiles = append(tiles, tile)
			}
		}
		/*
			A placeable output records the tile it draws and the style within it,
			so an item with no icon file of its own can be drawn from the tile
			sheet. The first recipe to make an item decides, because the later
			ones say the same thing.
		*/
		if ct, ok := readI32(mem, made+uint32(layout.ItemCreateTile)); ok && ct >= 0 { //nolint:gosec // a field offset
			key := fmt.Sprint(out)
			if _, already := book.TileIcons[key]; !already {
				style, _ := readI32(mem, made+uint32(layout.ItemPlaceStyle)) //nolint:gosec // a field offset
				book.TileIcons[key] = []int{int(ct), int(style)}
			}
		}
		book.Recipes = append(book.Recipes, rec)
	}
	book.Stations = tileNames(mem, tiles)
	return book, nil
}

// ingredients is what one recipe takes, as [item, count] pairs.
func ingredients(mem Mem, items uint32) [][]int {
	out := [][]int{}
	if items == 0 {
		return out
	}
	n, ok := readI32(mem, items+layout.ArrLenOff)
	if !ok || n <= 0 {
		return out
	}
	if n > MaxIngredients {
		n = MaxIngredients
	}
	for j := range int(n) {
		obj, ok := readU32(mem, items+layout.ArrDataOff+uint32(j)*4) //nolint:gosec // an index
		if !ok || obj == 0 {
			continue
		}
		itemType, ok := readI32(mem, obj+uint32(layout.ItemType)) //nolint:gosec // a field offset
		if !ok || itemType == 0 {
			continue
		}
		stack, _ := readI32(mem, obj+uint32(layout.ItemStack)) //nolint:gosec // a field offset
		if stack == 0 {
			stack = 1
		}
		out = append(out, []int{int(itemType), int(stack)})
	}
	return out
}

/*
tileNames is the game's own display name for each station tile.

Empty when the two patterns cannot be found, which a caller reports as "Tile #N"
rather than failing: a book with unnamed stations is still a book.
*/
func tileNames(mem Mem, tiles []int32) map[string]string {
	out := map[string]string{}
	lookupAt, haveLookup := resolveOperand(mem, tileLookupPattern)
	legendAt, haveLegend := resolveOperand(mem, mapLegendPattern)
	if !haveLookup || !haveLegend {
		return out
	}
	lookup, _ := readU32(mem, lookupAt)
	legend, _ := readU32(mem, legendAt)
	if lookup == 0 || legend == 0 {
		return out
	}
	lookupLen, _ := readI32(mem, lookup+layout.ArrLenOff)
	legendLen, _ := readI32(mem, legend+layout.ArrLenOff)

	for _, tile := range tiles {
		if tile < 0 || tile >= lookupLen {
			continue
		}
		raw := mem.Read(lookup+layout.ArrDataOff+uint32(tile)*2, 2) //nolint:gosec // an index
		if len(raw) < 2 {
			continue
		}
		idx := int32(binary.LittleEndian.Uint16(raw))
		if idx < 0 || idx >= legendLen {
			continue
		}
		text, ok := readU32(mem, legend+layout.ArrDataOff+uint32(idx)*4) //nolint:gosec // an index
		if !ok || text == 0 {
			continue
		}
		str, _ := readU32(mem, text+uint32(LocalizedTextValue)) //nolint:gosec // a field offset
		// A name that cannot be read is no name. The reader already refuses an
		// empty or non-printable string, so there is nothing more to test here.
		if name, got := locate.ReadMonoString(mem, str); got {
			out[fmt.Sprint(tile)] = name
		}
	}
	return out
}

/*
resolveOperand is the absolute address a `mov eax,[abs]` loads, found by its
surrounding code.

A pattern that matches more than once resolves to nothing: two candidates mean
the shape is no longer unique to the instruction that was meant, and guessing
between them would name the wrong static.
*/
func resolveOperand(mem Mem, pat patch.Pattern) (uint32, bool) {
	seedOff, seed := pat.Seed()
	var found uint32
	hits := 0
	for _, region := range mem.ExecRegions() {
		buf := mem.Read(region.Start, region.Size())
		for i := 0; i+len(seed) <= len(buf); i++ {
			if !hasPrefix(buf[i:], seed) {
				continue
			}
			pos := i - seedOff
			if pos < 0 || !pat.Matches(buf, pos) {
				continue
			}
			hits++
			if hits > 1 {
				return 0, false
			}
			found = region.Start + uint32(pos) //nolint:gosec // an offset in a 32-bit region
		}
	}
	if hits == 0 {
		return 0, false
	}
	// The operand of `mov eax,[abs]`, which is two bytes into the instruction.
	return readU32Or(mem, found+2)
}

func hasPrefix(buf, want []byte) bool {
	if len(buf) < len(want) {
		return false
	}
	for i, b := range want {
		if buf[i] != b {
			return false
		}
	}
	return true
}

/*
Save writes the book where the game package reads it from.

Written with no spaces, as the Python writes it: the file is in the repository
and a reformat would be a diff of every recipe in it.
*/
func Save(book game.Recipes, path string) error {
	raw, err := json.Marshal(struct {
		Recipes   []game.Recipe     `json:"recipes"`
		Stations  map[string]string `json:"stations"`
		TileIcons map[string][]int  `json:"tileicons"`
	}{book.Recipes, book.Stations, book.TileIcons})
	if err != nil {
		return fmt.Errorf("encode the recipe book: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("make the data directory: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil { //nolint:gosec // a file in the repository
		return fmt.Errorf("write the recipe book: %w", err)
	}
	proc.GiveBackToUser(path)
	return nil
}

func readU32(mem Mem, addr uint32) (uint32, bool) { return mem.ReadU32(addr) }
func readI32(mem Mem, addr uint32) (int32, bool)  { return mem.ReadI32(addr) }
func readU32Or(mem Mem, addr uint32) (uint32, bool) {
	v, ok := mem.ReadU32(addr)
	return v, ok && v != 0
}

/*
DataPath is where the bundled book lives in the working tree.

Relative to the current directory on purpose: this is a maintenance command run
from a checkout, and writing into an installed copy's embedded data would put
the file somewhere the build does not read.
*/
func DataPath() string { return filepath.Join("data", "recipes.json") }
