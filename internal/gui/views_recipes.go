package gui

import (
	"context"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/dialogs"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
recipeIconSize and recipeCellWidth are one grid cell, from the Qt panel.

GridWrap sizes every cell from the template's minimum, and an icon is narrower
than the name under it, so the width is set here rather than left to the icon:
without it a cell is 40 wide and every label runs into its neighbour.
*/
const (
	recipeIconSize  float32 = 40
	recipeCellWidth float32 = 96
)

// The two ways of reading the recipe book.
const (
	modeMakes = "Makes" // pick a craftable item, see how it is made
	modeUses  = "Uses"  // pick an ingredient, see what it makes
)

// recipeState is the book and what the section is showing of it.
type recipeState struct {
	book *client.Recipes
	// makes and uses index the book by item, built once when it is read:
	// filtering runs on every keystroke and walking 3,600 recipes per stroke is
	// work that does not need doing twice.
	makes  map[int][]client.Recipe
	uses   map[int][]client.Recipe
	ids    []int // every item the current mode knows about, sorted by name
	mode   string
	filter string
}

// buildRecipes is the crafting book: what makes what, and what it takes.
func (u *ui) buildRecipes() fyne.CanvasObject {
	if u.rc.mode == "" {
		u.rc.mode = modeMakes
	}

	mode := widget.NewSelect([]string{modeMakes, modeUses}, nil)
	mode.SetSelected(u.rc.mode)
	filter := widget.NewEntry()
	filter.SetPlaceHolder("filter by item name or ItemID")
	filter.SetText(u.rc.filter)

	rows := u.visibleRecipeItems()
	count := widget.NewLabel("")
	setCount := func() {
		count.SetText(fmt.Sprintf("%d of %d item(s)", len(rows), len(u.rc.ids)))
	}
	setCount()

	grid := widget.NewGridWrap(
		func() int { return len(rows) },
		newRecipeCell,
		func(i widget.GridWrapItemID, o fyne.CanvasObject) {
			if i < len(rows) {
				u.fillRecipeCell(o, rows[i])
			}
		},
	)
	grid.OnSelected = func(i widget.GridWrapItemID) {
		if i < len(rows) {
			u.showRecipe(rows[i])
		}
		grid.UnselectAll()
	}

	refresh := func() {
		rows = u.visibleRecipeItems()
		setCount()
		grid.Refresh()
	}
	mode.OnChanged = func(v string) { u.rc.mode = v; u.indexRecipes(); refresh() }
	filter.OnChanged = func(v string) { u.rc.filter = v; refresh() }

	extract := widget.NewButton("Re-extract from the game", func() { u.extractRecipes() })
	icons := widget.NewButton("Extract item icons", func() { u.extractSprites() })
	u.sh.Gate(extract, icons)

	var body fyne.CanvasObject = grid
	if u.rc.book == nil || len(u.rc.book.Recipes) == 0 {
		body = container.NewVScroll(widgets.Card("Recipes",
			widgets.Wrapped("The recipe book has not been read yet."),
			widgets.DimWrapped("Re-extract from the game reads it once. After that it works "+
				"offline, so this only needs doing again after a game update."),
		))
	}

	head := container.NewBorder(nil, nil,
		widgets.WithTip(widgets.FixedWidth(mode, modeWidth),
			"Makes: pick a craftable item to see how it is made. "+
				"Uses: pick an ingredient to see what it makes."),
		nil, filter)

	return u.logSplit(container.NewBorder(
		container.NewVBox(widgets.Heading("Recipes", "Browse craftable items."), head),
		container.NewHBox(count,
			widgets.WithTip(extract, "The recipe book is read from the game once and "+
				"cached. Re-extract after a game update."),
			widgets.WithTip(icons, "Item icons are decoded from the game's own files "+
				"into ~/.cache. It takes around half a minute, once.")),
		nil, nil, body))
}

// modeWidth is the Makes/Uses picker's width. The recipe dialog is sized to
// hold a handful of recipes without scrolling, which is what most items have.
const (
	modeWidth     float32 = 140
	recipeDialogW float32 = 620
	recipeDialogH float32 = 420
)

// newRecipeCell is the blank a grid cell is recycled from. GridWrap reuses
// these, so the shape is built once and only its contents change.
func newRecipeCell() fyne.CanvasObject {
	icon := canvas.NewImageFromResource(nil)
	icon.FillMode = canvas.ImageFillContain
	icon.ScaleMode = canvas.ImageScalePixels
	icon.SetMinSize(fyne.NewSize(recipeIconSize, recipeIconSize))

	label := canvas.NewText("", color.NRGBA{R: 230, G: 230, B: 230, A: 255})
	label.TextSize = 10
	label.Alignment = fyne.TextAlignCenter

	return widgets.FixedWidth(container.NewVBox(container.NewCenter(icon), label), recipeCellWidth)
}

// fillRecipeCell writes one item into a recycled cell.
func (u *ui) fillRecipeCell(o fyne.CanvasObject, itemID int) {
	// The cell is the fixed-width wrapper: spacer first, then the column.
	pad, ok := o.(*fyne.Container)
	if !ok || len(pad.Objects) != 2 {
		return
	}
	box, ok := pad.Objects[1].(*fyne.Container)
	if !ok || len(box.Objects) != 2 {
		return
	}
	centre, _ := box.Objects[0].(*fyne.Container)
	label, _ := box.Objects[1].(*canvas.Text)
	if centre == nil || label == nil || len(centre.Objects) == 0 {
		return
	}
	icon, _ := centre.Objects[0].(*canvas.Image)
	if icon == nil {
		return
	}

	icon.Resource = u.sprites.icon(itemID)
	icon.Refresh()
	label.Text = abbrev(u.itemName(itemID), abbrevWidth)
	label.Refresh()
}

/*
visibleRecipeItems is the items the current mode and filter leave.

Matched on name and on id, because a player who knows an item by its number
should be able to type it.
*/
func (u *ui) visibleRecipeItems() []int {
	want := strings.ToLower(strings.TrimSpace(u.rc.filter))
	if want == "" {
		return u.rc.ids
	}
	out := make([]int, 0, len(u.rc.ids))
	for _, id := range u.rc.ids {
		if strings.Contains(strings.ToLower(u.itemName(id)), want) ||
			strings.Contains(strconv.Itoa(id), want) {
			out = append(out, id)
		}
	}
	return out
}

/*
indexRecipes builds the two lookups and the sorted list the grid walks.

Once per read rather than per keystroke: there are some 3,600 recipes, and the
filter runs on every character typed.
*/
func (u *ui) indexRecipes() {
	if u.rc.book == nil {
		u.rc.ids = nil
		return
	}
	if u.rc.makes == nil {
		u.rc.makes = map[int][]client.Recipe{}
		u.rc.uses = map[int][]client.Recipe{}
		for _, r := range u.rc.book.Recipes {
			u.rc.makes[r.Out] = append(u.rc.makes[r.Out], r)
			for _, ing := range r.Ing {
				if len(ing) > 0 {
					u.rc.uses[ing[0]] = append(u.rc.uses[ing[0]], r)
				}
			}
		}
	}

	from := u.rc.makes
	if u.rc.mode == modeUses {
		from = u.rc.uses
	}
	ids := make([]int, 0, len(from))
	for id := range from {
		ids = append(ids, id)
	}
	// By name, because the grid is read by eye. Ties fall back to the id so the
	// order is stable between reads.
	sort.Slice(ids, func(i, j int) bool {
		a, b := u.itemName(ids[i]), u.itemName(ids[j])
		if a == b {
			return ids[i] < ids[j]
		}
		return a < b
	})
	u.rc.ids = ids
}

// showRecipe opens what an item is made of, or what it goes into.
func (u *ui) showRecipe(itemID int) {
	list := u.rc.makes[itemID]
	title := "How to make " + u.itemName(itemID)
	if u.rc.mode == modeUses {
		list = u.rc.uses[itemID]
		title = u.itemName(itemID) + " is used in"
	}
	if len(list) == 0 {
		u.sh.Flash("No recipe for "+u.itemName(itemID)+".", fd.StatusInfo)
		return
	}

	body := container.NewVBox()
	for _, r := range list {
		parts := make([]string, 0, len(r.Ing))
		for _, ing := range r.Ing {
			if len(ing) < 2 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s x%d", u.itemName(ing[0]), ing[1]))
		}
		made := fmt.Sprintf("%s x%d", u.itemName(r.Out), r.N)
		body.Add(widgets.PlainRow(made, strings.Join(parts, ", ")))
		body.Add(widgets.DimWrapped("at: " + r.Station(u.rc.book.Stations)))
	}

	dialogs.ShowDetail(u.sh.Window, title, container.NewVScroll(body), recipeDialogW, recipeDialogH)
}

// extractRecipes reads the recipe book out of the game, once.
func (u *ui) extractRecipes() {
	u.sh.Perform("Reading the recipes...", func(ctx context.Context) error {
		out, err := u.runUser(ctx, client.ExtractRecipesArgv())
		fyne.Do(func() {
			for _, line := range splitLines(out) {
				u.note(line)
			}
			if err == nil {
				u.loadRecipes()
			}
		})
		return err
	})
}

/*
extractSprites builds the item icon cache.

Unprivileged, and that is not tidiness: it writes under the user's home, and run
through sudo the cache would belong to root and the extractor could never
rewrite it. It takes around half a minute the first time.
*/
func (u *ui) extractSprites() {
	u.sh.Perform("Extracting item icons...", func(ctx context.Context) error {
		out, err := u.runUser(ctx, client.ExtractSpritesArgv(false))
		fyne.Do(func() {
			for _, line := range splitLines(out) {
				u.note(line)
			}
			if err == nil {
				u.sprites = newSprites() // a fresh cache, so forget the misses
				u.redrawAllCells()
				u.sh.Flash("Item icons extracted.", fd.StatusGood)
				u.sh.Refresh()
			}
		})
		return err
	})
}

// loadRecipes reads the cached book. Static and unprivileged, so it goes
// straight to the CLI.
func (u *ui) loadRecipes() {
	u.sh.Load("Reading the recipes...", func(ctx context.Context) error {
		out, err := u.runUser(ctx, client.RecipesArgv())
		book, ok := client.ParseRecipes(out)
		if !ok {
			if err != nil {
				fyne.Do(func() { u.note("recipes: " + firstLine(detail(out, err))) })
			}
			return nil
		}
		fyne.Do(func() {
			u.rc.book, u.rc.makes, u.rc.uses = book, nil, nil
			u.indexRecipes()
			u.sh.Refresh()
		})
		return nil
	})
}
