package game

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/ushineko/terrariabonker/data"
)

/*
Recipes is the crafting book, as it was read out of the game.

Three tables: 3,603 recipes, the 35 crafting stations by tile id, and the icon
each tile is drawn with. `extract-recipes` reads them out of the running game
once; this reads the file it wrote, which is bundled, so browsing the book needs
no game and no privilege.
*/
type Recipes struct {
	Recipes []Recipe
	// Stations is a tile id to the station's name. Keyed by the number as text,
	// which is how the game's own table is written down.
	Stations map[string]string
	// TileIcons is the tile id to the item that draws it: [type, style].
	TileIcons map[string][]int
}

// Recipe is one entry of the book: what it makes, how many, what it takes, and
// the tile it has to be made at.
type Recipe struct {
	Out int `json:"out"`
	N   int `json:"n"`
	// Ing is [item, count] pairs.
	Ing [][]int `json:"ing"`
	/*
		Tile is the crafting station, or nil for something made by hand.

		Omitted rather than written as null, which is how the bundled file
		spells it: most recipes are made by hand, and a null apiece would be a
		fifth of the file saying nothing.
	*/
	Tile *int `json:"tile,omitempty"`
}

var (
	recipeOnce sync.Once
	recipeBook *Recipes
	recipeErr  error
)

// CraftingBook is the recipe tables, loaded on the first call.
func CraftingBook() (*Recipes, error) {
	recipeOnce.Do(func() {
		raw, err := data.FS.ReadFile(data.Recipes)
		if err != nil {
			recipeErr = fmt.Errorf("read %s: %w", data.Recipes, err)
			return
		}
		var book Recipes
		if err := json.Unmarshal(raw, &book); err != nil {
			recipeErr = fmt.Errorf("decode %s: %w", data.Recipes, err)
			return
		}
		// An absent table is an empty one, not a nil map: a caller looking up a
		// station in a book that has none should get "made by hand", not a
		// panic.
		if book.Stations == nil {
			book.Stations = map[string]string{}
		}
		if book.TileIcons == nil {
			book.TileIcons = map[string][]int{}
		}
		recipeBook = &book
	})
	return recipeBook, recipeErr
}

// UnmarshalJSON reads the file's own field names.
func (r *Recipes) UnmarshalJSON(b []byte) error {
	var doc struct {
		Recipes   []Recipe          `json:"recipes"`
		Stations  map[string]string `json:"stations"`
		TileIcons map[string][]int  `json:"tileicons"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("decode the recipe book: %w", err)
	}
	r.Recipes, r.Stations, r.TileIcons = doc.Recipes, doc.Stations, doc.TileIcons
	return nil
}

/*
Station names where a recipe is made, or says it is made by hand.

A tile the book has no name for is reported as its number rather than as
nothing: a recipe that has to be made *somewhere* and cannot say where is worse
than one that names a tile the reader can look up.
*/
func (r *Recipes) Station(rec Recipe) string {
	if rec.Tile == nil {
		return "by hand"
	}
	key := strconv.Itoa(*rec.Tile)
	if name := r.Stations[key]; name != "" {
		return name
	}
	return "tile " + key
}

// Makes is every recipe that produces an item.
func (r *Recipes) Makes(item int) []Recipe {
	var out []Recipe
	for _, rec := range r.Recipes {
		if rec.Out == item {
			out = append(out, rec)
		}
	}
	return out
}

// Uses is every recipe that takes an item as an ingredient.
func (r *Recipes) Uses(item int) []Recipe {
	var out []Recipe
	for _, rec := range r.Recipes {
		for _, ing := range rec.Ing {
			if len(ing) > 0 && ing[0] == item {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}
