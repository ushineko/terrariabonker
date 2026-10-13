package gui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/game"
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
Right-clicking a cell opens its menu, which is where the sell list is reached.
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

	hint := widgets.DimWrapped("Click a slot to edit it, or an empty slot to place an item. " +
		"Right-click an item to put it on the sell list, or to take it off.")
	if !u.sprites.ready() {
		hint = widgets.DimWrapped("Click a slot to edit it; right-click an item for the sell " +
			"list. Item icons are not extracted yet, so slots show shortened names.")
	}

	return u.logSplit(container.NewBorder(
		container.NewVBox(
			widgets.Heading("Inventory", "Edit carried items."),
			hint,
		),
		nil, nil, nil,
		container.NewVScroll(body)))
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

	parts = append(parts, newSlotButton(
		func() { u.editSlot(slot) },
		func() *fyne.Menu { return u.cellMenu(slot) }))

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
		fyne.Do(func() {
			// One read feeds both sections: the cells want names and the
			// Compendium wants the rest of it, and the read costs a scan of the
			// game's item templates.
			u.takeCatalog(cat)
			u.sh.Refresh()
		})
		return nil
	})
}

/*
loadItemNames reads the bundled name table, in this process.

It used to be a subprocess: the table lived in the Python package and a front end
in another language could not reach it, so the window asked the CLI for a copy at
every start-up. The table is JSON extracted from the game's own files, embedded
in this binary now (spec 051, step 1), so there is nothing to ask anyone for.

Names arrive before the window is drawn rather than a subprocess later, and they
arrive with no game running, which is what a recipe book that works offline needs.
*/
func (u *ui) loadItemNames() {
	names, err := game.ItemNames()
	if err != nil {
		u.note("item names: " + err.Error())
		return
	}
	u.mergeNames(names.All())
}

// mergeNames adds names without dropping any already known. The compendium's
// come from the running game and the table's are bundled; neither is a reason
// to forget the other.
func (u *ui) mergeNames(names map[int]string) {
	if u.iv.names == nil {
		u.iv.names = make(map[int]string, len(names))
	}
	for id, name := range names {
		if name != "" {
			u.iv.names[id] = name
		}
	}
}

// loadPrefixes reads the modifier catalog, in this process: it is bundled data
// (spec 051, step 1), so there is nobody to ask for it.
func (u *ui) loadPrefixes() {
	list, err := game.ItemPrefixes()
	if err != nil {
		u.note("modifiers: " + err.Error())
		return
	}
	byID := make(map[int]client.Prefix, len(list.All()))
	for _, id := range list.All() {
		byID[id] = client.Prefix{ID: id, Name: list.Name(id), Quality: list.Quality(id)}
	}
	u.iv.prefix = byID
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
slotButton is a cell's click target: a button that also answers a right-click.

Fyne's Button is not secondary-tappable, so this adds the one method. It sits
under the cell's tip, which takes the hover and neither kind of tap, so both
reach it.
*/
type slotButton struct {
	widget.Button
	menu func() *fyne.Menu
}

func newSlotButton(tapped func(), menu func() *fyne.Menu) *slotButton {
	b := &slotButton{menu: menu}
	b.OnTapped = tapped
	b.Importance = widget.LowImportance
	b.ExtendBaseWidget(b)
	return b
}

// TappedSecondary implements fyne.SecondaryTappable: the cell's menu, at the
// pointer.
func (b *slotButton) TappedSecondary(e *fyne.PointEvent) {
	c := fyne.CurrentApp().Driver().CanvasForObject(b)
	if c == nil || b.menu == nil {
		return
	}
	// The pointer has not left the cell, so nothing else takes its tip down,
	// and it would sit on top of the menu.
	widgets.HideTips()
	widget.ShowPopUpMenuAtPosition(b.menu(), c, e.AbsolutePosition)
}

/*
cellMenu is what right-clicking a slot offers.

The sell list is reached from here and not from the editor: the editor is about
one item in one slot, and the list is about every item of a kind, wherever it
turns up. An empty slot has no kind to list.
*/
func (u *ui) cellMenu(slot int) *fyne.Menu {
	s := u.slotAt(slot)
	if s.Empty() {
		return fyne.NewMenu("",
			fyne.NewMenuItem("Place an item…", func() { u.editSlot(slot) }))
	}
	label := "Add to the sell list"
	if u.listedForSale(s.Type) {
		label = "Take off the sell list"
	}
	return fyne.NewMenu("",
		fyne.NewMenuItem("Edit "+u.itemName(s.Type)+"…", func() { u.editSlot(slot) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(label, func() { u.toggleSellList(s.Type) }),
	)
}

// slotAt is the last reading of a slot, or an empty one when it has not been
// read yet.
func (u *ui) slotAt(slot int) client.ItemSlot {
	if s, ok := u.iv.slots[slot]; ok {
		return s
	}
	return client.ItemSlot{Slot: slot}
}

/*
toggleSellList puts an item type on the sell list, or takes it off.

The command that changes the list also answers with it, so the grid is redrawn
from what the profile now holds rather than from what was asked for.
*/
func (u *ui) toggleSellList(itemType int) {
	name := u.itemName(itemType)
	argv := client.SellListArgv(&itemType, nil)
	if u.listedForSale(itemType) {
		argv = client.SellListArgv(nil, &itemType)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), onceTimeout)
		defer cancel()
		out, err := u.run(ctx, argv)
		fyne.Do(func() {
			list, ok := client.ParseSellList(out)
			if err != nil || !ok {
				u.note("[sell] the sell list was not changed: " + firstLine(detail(out, err)))
				return
			}
			u.takeSellList(list)
			if u.listedForSale(itemType) {
				u.note("[sell] " + name + " is on the sell list")
			} else {
				u.note("[sell] " + name + " is off the sell list")
			}
		})
	}()
}

/*
takeSellList adopts a freshly read sell list, and redraws the cells it re-marks.

A cell is otherwise redrawn only when its slot changes, and listing an item
changes no slot.
*/
func (u *ui) takeSellList(list []int) {
	sort.Ints(list)
	before := map[int]bool{}
	for _, t := range u.fx.whitelist {
		before[t] = true
	}
	u.fx.whitelist = list
	if u.fx.sellList != nil {
		u.fx.sellList.Refresh()
	}
	for slot, s := range u.iv.slots {
		if !s.Empty() && before[s.Type] != u.listedForSale(s.Type) {
			u.drawCell(slot)
		}
	}
}

/*
editSlot opens the editor for one slot.

The type the slot held when the editor opened is carried into the write as
--expect-type, because the grid re-reads every second: without it an edit built
on a snapshot could land on whatever has since replaced the item.
*/
func (u *ui) editSlot(slot int) {
	s := u.slotAt(slot)
	f := newItemFields(u, s)

	title := "Slot " + strconv.Itoa(slot)
	if !s.Empty() {
		title += " — " + u.fullName(s)
	}
	u.prompt(title, "Write", f.body, func() {
		now, err := f.read()
		if err != nil {
			u.sh.Flash(err.Error(), fd.StatusWarn)
			return
		}
		e, write := editOf(s, now)
		if !write {
			u.sh.Flash("Nothing was changed.", fd.StatusWarn)
			return
		}
		u.do("Writing slot "+strconv.Itoa(slot), client.SetItemArgv(slot, now.Type, e))
	})
}

// itemFields is the editor's widgets, and how to read them back.
type itemFields struct {
	body fyne.CanvasObject
	read func() (itemForm, error)
}

// The ranges the editor accepts. Wide: these are the game's own 32-bit fields,
// and the limits are there to catch a slip of the keyboard, not to judge.
const (
	maxItemStack = 9999
	maxDamage    = 999999
	maxUseTicks  = 999
	maxPickPower = 2000
	maxTileBoost = 100
)

// errNoSuchItem is what the editor says when the Item field names nothing.
var errNoSuchItem = errors.New("that is not an item name or number")

/*
newItemFields builds the editor's form for one slot.

Every field the CLI's set-item writes is here. The stats belong to the item the
slot holds, so they are switched off while the Item field names a different one:
that write places the new item with its own stats, and these would be ignored.
*/
func newItemFields(u *ui, s client.ItemSlot) itemFields {
	was := formOf(s)
	names, _ := game.ItemNames()

	item := widget.NewEntry()
	item.SetPlaceHolder("item name or number")
	if !s.Empty() {
		item.SetText(strconv.Itoa(s.Type))
	}
	means := widget.NewLabel("")
	means.Truncation = fyne.TextTruncateEllipsis
	typed := func() (int, bool) {
		if s.Empty() && strings.TrimSpace(item.Text) == "" {
			return 0, true
		}
		return resolveItem(names, item.Text)
	}

	number := func(v, lo, hi int) *widget.Entry {
		e := widget.NewEntry()
		e.SetText(strconv.Itoa(v))
		e.Validator = numberIn(float64(lo), float64(hi))
		return e
	}
	stack := number(was.Stack, 1, maxItemStack)
	damage := number(was.Damage, -1, maxDamage)
	defense := number(was.Defense, 0, maxItemStack)
	useTime := number(was.UseTime, 0, maxUseTicks)
	useAnim := number(was.UseAnim, 0, maxUseTicks)
	pick := number(was.Pick, 0, maxPickPower)
	tile := number(was.TileBoost, 0, maxTileBoost)
	auto := widget.NewCheck("swing or fire while the button is held", nil)
	auto.SetChecked(was.AutoReuse)

	ids := []int{0}
	labels := []string{"none"}
	if all, err := game.ItemPrefixes(); err == nil {
		offered := prefixChoices(all, s)
		ids = append(ids, offered...)
		labels = append(labels, prefixLabels(offered, u.iv.prefix)...)
	}
	prefix := widget.NewSelect(labels, nil)
	prefix.SetSelectedIndex(0)
	for i, id := range ids {
		if id == was.Prefix {
			prefix.SetSelectedIndex(i)
		}
	}
	takesModifier := len(ids) > 1

	// The stats of the item in the slot: meaningless for an empty slot, and
	// for a slot about to hold something else.
	stats := []fyne.Disableable{damage, defense, useTime, useAnim, pick, tile, auto}
	sync := func() {
		t, ok := typed()
		switch {
		case !ok:
			means.SetText("no item by that name or number")
		case t == 0 && s.Empty():
			means.SetText("")
		case t == 0:
			means.SetText("empties the slot")
		default:
			means.SetText(u.itemName(t) + " (#" + strconv.Itoa(t) + ")")
		}
		same := ok && t == s.Type && !s.Empty()
		for _, w := range stats {
			setEnabled(w, same)
		}
		setEnabled(prefix, same && takesModifier)
		setEnabled(stack, ok && t != 0)
	}
	item.OnChanged = func(string) { sync() }
	sync()

	note := "Type a different item to replace this one. It arrives with its own stats, " +
		"which can then be edited."
	if s.Empty() {
		note = "The item arrives with its own stats, which can then be edited."
	}
	form := container.New(layout.NewFormLayout(),
		widget.NewLabel("Item"), container.NewBorder(nil, means, nil, nil, item),
		widget.NewLabel("Stack"), stack,
		widget.NewLabel("Damage"), damage,
		widget.NewLabel("Defense"), defense,
		widget.NewLabel("Modifier"), prefix,
		widget.NewLabel("Auto-reuse"), auto,
		widget.NewLabel("Use time"), widgets.WithTip(useTime, "Ticks per use. Lower is faster."),
		widget.NewLabel("Use animation"), widgets.WithTip(useAnim, "Ticks one swing is drawn for."),
		widget.NewLabel("Pickaxe power %"), pick,
		widget.NewLabel("Placement reach"), widgets.WithTip(tile, "Extra tiles of reach while this item is held."),
	)

	read := func() (itemForm, error) {
		t, ok := typed()
		if !ok {
			return itemForm{}, errNoSuchItem
		}
		now := itemForm{Type: t, AutoReuse: auto.Checked}
		if i := prefix.SelectedIndex(); i >= 0 && i < len(ids) {
			now.Prefix = ids[i]
		}
		for _, f := range []struct {
			name string
			from *widget.Entry
			to   *int
		}{
			{"stack", stack, &now.Stack}, {"damage", damage, &now.Damage},
			{"defense", defense, &now.Defense}, {"use time", useTime, &now.UseTime},
			{"use animation", useAnim, &now.UseAnim}, {"pickaxe power", pick, &now.Pick},
			{"placement reach", tile, &now.TileBoost},
		} {
			if f.from.Disabled() {
				// Switched off because it does not apply to this write. What
				// it shows is the old item's, and editOf does not look at it.
				continue
			}
			if err := f.from.Validate(); err != nil {
				return itemForm{}, fmt.Errorf("%s wants a %w", f.name, err)
			}
			*f.to, _ = strconv.Atoi(strings.TrimSpace(f.from.Text))
		}
		return now, nil
	}
	return itemFields{body: container.NewVBox(form, widgets.DimWrapped(note)), read: read}
}

// setEnabled switches a control on or off.
func setEnabled(w fyne.Disableable, on bool) {
	if on {
		w.Enable()
		return
	}
	w.Disable()
}
