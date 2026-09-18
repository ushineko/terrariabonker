package gui

import (
	"context"
	"image/color"
	"sort"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// The grid's geometry, from the Qt panel: a cell wide enough for a stack count
// beside an icon, and an icon that fills it minus the border.
const (
	cellWidth  float32 = 66
	cellHeight float32 = 46
	iconInset  float32 = 12
	// abbrevWidth is how many characters a cell's fallback label gets when the
	// sprite cache has no icon for an item.
	abbrevWidth = 8
)

// inventoryEvery is how often the grid re-reads the game. The Qt panel's 1 Hz:
// fast enough that an edit is never built on a stale snapshot, and affordable
// only because the warm worker answers in milliseconds.
const inventoryEvery = time.Second

// inventoryState is what the section draws and what the sync writes.
type inventoryState struct {
	slots map[int]client.ItemSlot
	// shown is what each cell was last drawn from, so an unchanged cell is left
	// alone. Redrawing one resets its icon and cancels a tip being read.
	shown map[int]client.ItemSlot
	cells map[int]*fyne.Container
	names map[int]string
	// namesOK is set once the catalog has been read, so the grid stops asking.
	namesOK bool
	prefix  map[int]client.Prefix
}

/*
buildInventory is the player's own inventory, slot for slot.

The layout is Terraria's: a hotbar, the bag, the coin slots and the ammo slots.
Clicking a cell edits that slot; clicking an empty one places something in it.
*/
func (u *ui) buildInventory() fyne.CanvasObject {
	u.iv.cells = map[int]*fyne.Container{}

	body := container.NewVBox()
	for _, sec := range gridSections {
		grid := container.New(layout.NewGridLayoutWithColumns(sec.Cols))
		for slot := sec.From; slot < sec.To; slot++ {
			cell := container.NewStack()
			u.iv.cells[slot] = cell
			u.drawCell(slot)
			// Each cell is pinned, and the grid is left-aligned with a spacer
			// rather than stretched across the window. A grid layout divides
			// whatever width it is given, so without both a slot would be as
			// wide as a tenth of the window and an inventory would look
			// nothing like the game's.
			grid.Add(fixedSize(cell, cellWidth, cellHeight))
		}
		body.Add(widgets.Card(sec.Title, container.NewHBox(grid, layout.NewSpacer())))
	}

	hint := widgets.DimWrapped("Click a slot to edit it, or an empty slot to place an item.")
	if !u.sprites.ready() {
		hint = widgets.DimWrapped("Click a slot to edit it. Item icons are not extracted yet, " +
			"so slots show shortened names.")
	}

	return container.NewBorder(
		container.NewVBox(
			widgets.Heading("Inventory", "Edit carried items."),
			hint,
		),
		widgets.FixedHeight(u.logWidget(), logHeight), nil, nil,
		container.NewVScroll(body))
}

/*
drawCell paints one slot.

Called for a slot whose contents changed, not for every slot on every sync. The
grid re-reads once a second, and redrawing an unchanged cell resets its icon and
takes down a tip somebody is reading.
*/
func (u *ui) drawCell(slot int) {
	cell := u.iv.cells[slot]
	if cell == nil {
		return
	}
	s, known := u.iv.slots[slot]
	if !known {
		s = client.ItemSlot{Slot: slot}
	}

	bg, border := cellColors(s.Rare)
	if s.Empty() {
		// An empty slot is not a rarity. Plain, so the eye skips it.
		bg, border = color.NRGBA{R: 38, G: 38, B: 38, A: 255},
			color.NRGBA{R: 58, G: 58, B: 58, A: 255}
	}
	back := canvas.NewRectangle(bg)
	back.StrokeColor = border
	back.StrokeWidth = 1
	back.CornerRadius = 4
	if u.listedForSale(s.Type) && !s.Empty() {
		// The cell's colour already carries the item's rarity, so auto-sell is
		// marked by thickening the border rather than by recolouring it, which
		// would cost information to add some.
		back.StrokeColor = color.NRGBA{R: 255, G: 204, B: 0, A: 255}
		back.StrokeWidth = 2
	}

	parts := []fyne.CanvasObject{back}
	if !s.Empty() {
		if res := u.sprites.icon(s.Type); res != nil {
			icon := canvas.NewImageFromResource(res)
			icon.FillMode = canvas.ImageFillContain
			// Nearest neighbour: these are pixel-art sprites, and smoothing
			// them turns a sword into a smudge.
			icon.ScaleMode = canvas.ImageScalePixels
			icon.SetMinSize(fyne.NewSize(cellHeight-iconInset, cellHeight-iconInset))
			parts = append(parts, container.NewPadded(icon))
		} else {
			label := canvas.NewText(abbrev(u.itemName(s.Type), abbrevWidth), color.NRGBA{R: 240, G: 240, B: 240, A: 255})
			label.TextSize = 10
			label.Alignment = fyne.TextAlignCenter
			parts = append(parts, container.NewCenter(label))
		}
		if badge := stackBadge(s.Stack); badge != "" {
			b := canvas.NewText(badge, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			b.TextSize = 10
			b.Alignment = fyne.TextAlignTrailing
			parts = append(parts, container.NewVBox(layout.NewSpacer(), b))
		}
	}

	slotCopy := slot
	button := widget.NewButton("", func() { u.editSlot(slotCopy) })
	button.Importance = widget.LowImportance
	parts = append(parts, button)

	tip := cellTip(s, u.fullName(s), u.listedForSale(s.Type))
	cell.Objects = []fyne.CanvasObject{widgets.WithTip(container.NewStack(parts...), tip)}
	cell.Refresh()
	u.iv.shown[slot] = s
}

// fixedSize pins an object's minimum size in both directions. The library has
// one for each axis; a cell wants both.
func fixedSize(o fyne.CanvasObject, w, h float32) fyne.CanvasObject {
	pad := canvas.NewRectangle(color.Transparent)
	pad.SetMinSize(fyne.NewSize(w, h))
	return container.NewStack(pad, o)
}

// itemName is what an item is called, or its number when the catalog has not
// been read.
func (u *ui) itemName(itemType int) string {
	if n, ok := u.iv.names[itemType]; ok && n != "" {
		return n
	}
	return "#" + strconv.Itoa(itemType)
}

// fullName is the item's name with its modifier, the way the game writes it:
// "Fabled Slime Staff".
func (u *ui) fullName(s client.ItemSlot) string {
	name := u.itemName(s.Type)
	if p, ok := u.iv.prefix[s.Prefix]; ok && p.Name != "" {
		return p.Name + " " + name
	}
	return name
}

// listedForSale reports whether auto-sell would take this item.
func (u *ui) listedForSale(itemType int) bool {
	if itemType == 0 {
		return false
	}
	for _, t := range u.fx.whitelist {
		if t == itemType {
			return true
		}
	}
	return false
}

/*
syncInventory re-reads the inventory and redraws only what changed.

Through the warm worker, which is what makes a one-second cadence affordable:
the same read as a one-shot CLI run costs 2.7 s, which would not finish before
the next tick.
*/
func (u *ui) syncInventory() {
	u.inventory = newWatch(u, "inventory", inventoryEvery,
		client.InventoryArgv,
		func(raw string) { u.readInventory(raw) })
	u.inventory.set(true)
}

// readInventory takes a sync's reply and redraws the cells that moved.
func (u *ui) readInventory(raw string) {
	slots, ok := client.ParseSlots(raw)
	if !ok {
		return
	}
	if u.iv.slots == nil {
		u.iv.slots = map[int]client.ItemSlot{}
	}
	changed := make([]int, 0, 4)
	for _, s := range slots {
		u.iv.slots[s.Slot] = s
		if was, drawn := u.iv.shown[s.Slot]; !drawn || !sameSlot(was, s) {
			changed = append(changed, s.Slot)
		}
	}
	for _, slot := range changed {
		u.drawCell(slot)
	}
}

// loadNames reads the item catalog once, for the names the cells and the editor
// show. It needs the game, so it is quiet when there is none.
func (u *ui) loadNames() {
	if u.iv.namesOK {
		return
	}
	u.sh.Load("Reading the item catalog...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.CompendiumArgv(false))
		cat, ok := client.ParseCompendium(out)
		if !ok {
			if err != nil {
				fyne.Do(func() { u.note("item catalog: " + firstLine(detail(out, err))) })
			}
			return nil
		}
		names := make(map[int]string, len(cat.Items))
		for _, it := range cat.Items {
			names[it.ID] = it.Name
		}
		fyne.Do(func() {
			u.iv.names, u.iv.namesOK = names, true
			u.redrawAllCells()
		})
		return nil
	})
}

// loadPrefixes reads the modifier catalog. Static, so it goes direct rather than
// through the worker.
func (u *ui) loadPrefixes() {
	u.sh.Load("Reading the modifiers...", func(ctx context.Context) error {
		out, err := u.runDirect(ctx, client.PrefixesArgv())
		list, ok := client.ParsePrefixes(out)
		if !ok {
			if err != nil {
				fyne.Do(func() { u.note("modifiers: " + firstLine(detail(out, err))) })
			}
			return nil
		}
		byID := make(map[int]client.Prefix, len(list))
		for _, p := range list {
			byID[p.ID] = p
		}
		fyne.Do(func() { u.iv.prefix = byID })
		return nil
	})
}

// redrawAllCells repaints every cell, for when something that affects all of
// them arrives: the names, or a freshly extracted icon cache.
func (u *ui) redrawAllCells() {
	for slot := range u.iv.cells {
		delete(u.iv.shown, slot)
		u.drawCell(slot)
	}
}

/*
editSlot opens the editor for one slot.

The type the slot held when the editor opened is carried into the write as
--expect-type, because the grid re-reads every second: without it an edit built
on a snapshot could land on whatever has since replaced the item.
*/
func (u *ui) editSlot(slot int) {
	s := u.iv.slots[slot]

	itemType := widget.NewEntry()
	itemType.SetText(strconv.Itoa(s.Type))
	itemType.Validator = numberIn(0, 9999)

	stack := widget.NewEntry()
	stack.SetText(strconv.Itoa(max(s.Stack, 1)))
	stack.Validator = numberIn(1, 9999)

	names := make([]string, 0, len(u.iv.prefix)+1)
	names = append(names, "none")
	byName := map[string]int{"none": 0}
	ids := make([]int, 0, len(u.iv.prefix))
	for id := range u.iv.prefix {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		p := u.iv.prefix[id]
		label := p.Name + " (" + p.Quality + ")"
		names = append(names, label)
		byName[label] = id
	}
	prefix := widget.NewSelect(names, nil)
	prefix.SetSelected("none")
	if p, ok := u.iv.prefix[s.Prefix]; ok {
		prefix.SetSelected(p.Name + " (" + p.Quality + ")")
	}

	/*
		The sell list is reached from here rather than by right-clicking the cell.

		The Qt panel uses a context menu on the cell. Fyne's Button is not
		secondary-tappable, and the affordance was undiscoverable anyway -- a
		whitelisted item is sold on the next round, so it never stays in the grid
		long enough to right-click a second time to take it off again.
	*/
	sell := widget.NewButton("", nil)
	sell.Hidden = s.Empty()
	listed := u.listedForSale(s.Type)
	sell.SetText("Add to the sell list")
	if listed {
		sell.SetText("Take off the sell list")
	}
	sell.OnTapped = func() {
		t := s.Type
		if listed {
			u.once("Taking "+u.itemName(t)+" off the sell list", client.SellListArgv(nil, &t))
		} else {
			u.once("Adding "+u.itemName(t)+" to the sell list", client.SellListArgv(&t, nil))
		}
		u.loadSellList()
	}

	form := container.New(layout.NewFormLayout(),
		widget.NewLabel("Item"), itemType,
		widget.NewLabel("Stack"), stack,
		widget.NewLabel("Modifier"), prefix,
		widget.NewLabel("Auto-sell"), container.NewHBox(sell),
	)
	title := "Slot " + strconv.Itoa(slot)
	if !s.Empty() {
		title += " — " + u.fullName(s)
	}

	u.prompt(title, "Write", form, func() {
		want, err := strconv.Atoi(itemType.Text)
		if err != nil {
			u.sh.Flash("That is not an item number.", fd.StatusWarn)
			return
		}
		e := client.ItemEdit{}
		if n, err := strconv.Atoi(stack.Text); err == nil {
			e.Stack = &n
		}
		if id, ok := byName[prefix.Selected]; ok {
			e.Prefix = &id
		}
		if !s.Empty() {
			had := s.Type
			e.ExpectType = &had
		}
		u.do("Writing slot "+strconv.Itoa(slot), client.SetItemArgv(slot, want, e))
	})
}
