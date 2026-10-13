package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// terraBlade is a slot holding an edited melee weapon, as the inventory reports
// one.
func terraBlade() client.ItemSlot {
	return client.ItemSlot{
		Slot: 3, Type: 757, Stack: 1, Damage: 85, UseTime: 14, UseAnim: 14,
		Rare: 8, Prefix: 81, AutoReuse: 1,
		Flags: map[string]any{"melee": true, "accessory": false},
	}
}

/*
Every stat the CLI can write can be typed into the editor.

The Go editor shipped with the item, its stack and its modifier, and nothing
else: damage, defense, auto-reuse, use time, use animation, pickaxe power and
placement reach could be read in the tip and not changed. Each is a field here,
and each reaches set-item as its own flag.
*/
func TestEveryItemStatCanBeEdited(t *testing.T) {
	s := terraBlade()
	now := formOf(s)
	now.Stack, now.Damage, now.Defense = 2, 500, 7
	now.AutoReuse = false
	now.UseTime, now.UseAnim, now.Pick, now.TileBoost = 5, 6, 210, 9

	e, write := editOf(s, now)
	require.True(t, write)
	require.Equal(t, []string{
		"set-item", "3", "757", "--expect-type", "757",
		"--stack", "2", "--damage", "500", "--auto-reuse", "0",
		"--use-time", "5", "--use-anim", "6", "--pick", "210",
		"--tile-boost", "9", "--defense", "7",
	}, client.SetItemArgv(3, now.Type, e))
}

/*
Only what was changed is written.

The form opens showing the item's current stats. Sending them all back submits
the item's own damage as an explicit edit and re-applies its modifier, which
resets the item to its base stats first -- so changing one number would quietly
undo every other edit on the item (spec 046).
*/
func TestOnlyTheChangedFieldsAreWritten(t *testing.T) {
	s := terraBlade()

	_, write := editOf(s, formOf(s))
	require.False(t, write, "a form submitted as it opened is not an edit")

	now := formOf(s)
	now.Damage = 500
	e, write := editOf(s, now)
	require.True(t, write)
	require.Equal(t, []string{"set-item", "3", "757", "--expect-type", "757", "--damage", "500"},
		client.SetItemArgv(3, now.Type, e), "the modifier and the stack were not touched")

	now = formOf(s)
	now.Prefix = 0
	e, _ = editOf(s, now)
	require.Equal(t, []string{"set-item", "3", "757", "--expect-type", "757", "--prefix", "0"},
		client.SetItemArgv(3, now.Type, e), "taking a modifier off is an edit, though it is a zero")
}

// A different item is written as its type and stack, so the game's template
// supplies its stats: the numbers in the form belong to what was there before.
func TestADifferentItemArrivesWithItsOwnStats(t *testing.T) {
	s := terraBlade()
	now := formOf(s)
	now.Type, now.Stack, now.Damage = 9, 30, 500

	e, write := editOf(s, now)
	require.True(t, write)
	require.Equal(t, []string{"set-item", "3", "9", "--expect-type", "757", "--stack", "30"},
		client.SetItemArgv(3, now.Type, e))

	// Into an empty slot there is nothing to expect.
	empty := client.ItemSlot{Slot: 12}
	now = formOf(empty)
	now.Type = 9
	e, write = editOf(empty, now)
	require.True(t, write)
	require.Equal(t, []string{"set-item", "12", "9", "--stack", "1"},
		client.SetItemArgv(12, now.Type, e))

	// Item zero empties the slot, and an emptied slot has no stack.
	now = formOf(s)
	now.Type = 0
	e, write = editOf(s, now)
	require.True(t, write)
	require.Equal(t, []string{"set-item", "3", "0", "--expect-type", "757"},
		client.SetItemArgv(3, now.Type, e))

	_, write = editOf(empty, formOf(empty))
	require.False(t, write, "an empty slot left empty is nothing to write")
}

/*
The modifiers offered are the ones the item can take.

Against the bundled table rather than a fixture: the question is whether a sword
is offered an accessory's modifier, and that is a fact about the game's pools.
*/
func TestTheModifiersOfferedFitTheItem(t *testing.T) {
	all, err := game.ItemPrefixes()
	require.NoError(t, err)
	named := func(ids []int) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, all.Name(id))
		}
		return out
	}

	sword := client.ItemSlot{Slot: 0, Type: 757, Flags: map[string]any{"melee": true}}
	require.Contains(t, named(prefixChoices(all, sword)), "Legendary")
	require.NotContains(t, named(prefixChoices(all, sword)), "Warding")

	ring := client.ItemSlot{Slot: 1, Type: 53, Flags: map[string]any{"accessory": true}}
	require.Contains(t, named(prefixChoices(all, ring)), "Warding")
	require.NotContains(t, named(prefixChoices(all, ring)), "Legendary")

	wood := client.ItemSlot{Slot: 2, Type: 9, Flags: map[string]any{"melee": false}}
	require.Empty(t, prefixChoices(all, wood), "a block takes no modifier")

	require.Len(t, prefixChoices(all, client.ItemSlot{Slot: 4}), len(all.All()),
		"an empty slot has no class yet, so nothing is ruled out")

	// A modifier the item already carries is offered whatever the pools say,
	// or the editor would open on "none" for an item that has one.
	wood.Prefix = 81
	require.Equal(t, []int{81}, prefixChoices(all, wood))
}

// Two modifiers can share a name, and the dropdown is read back by position in
// a list of labels, so no two labels may be the same.
func TestEveryModifierLabelIsItsOwn(t *testing.T) {
	all, err := game.ItemPrefixes()
	require.NoError(t, err)
	known := map[int]client.Prefix{}
	names := map[string]int{}
	for _, id := range all.All() {
		known[id] = client.Prefix{ID: id, Name: all.Name(id), Quality: all.Quality(id)}
		names[all.Name(id)]++
	}
	shared := 0
	for _, n := range names {
		if n > 1 {
			shared++
		}
	}
	require.Positive(t, shared, "the table has modifiers that share a name, or this proves nothing")

	labels := prefixLabels(all.All(), known)
	seen := map[string]bool{}
	for _, l := range labels {
		require.Falsef(t, seen[l], "%q names two modifiers", l)
		seen[l] = true
	}
	require.Equal(t, []string{"#9999"}, prefixLabels([]int{9999}, known), "an unknown id is its number")
}

// An item is typed as a number or as a name.
func TestAnItemIsTypedByNameOrNumber(t *testing.T) {
	names, err := game.ItemNames()
	require.NoError(t, err)

	for text, want := range map[string]int{"757": 757, " 9 ": 9, "0": 0, "terra blade": 757, "Wood": 9} {
		got, ok := resolveItem(names, text)
		require.Truef(t, ok, "%q names an item", text)
		require.Equalf(t, want, got, "%q", text)
	}
	for _, text := range []string{"", "   ", "-4", "no such item anywhere"} {
		_, ok := resolveItem(names, text)
		require.Falsef(t, ok, "%q names nothing", text)
	}
}

// entryShowing is the form's entry holding text, which is how a test finds a
// field without knowing the order they were built in.
func entryShowing(t *testing.T, body fyne.CanvasObject, text string) *widget.Entry {
	t.Helper()
	for _, e := range fynetest.All[*widget.Entry](body) {
		if e.Text == text {
			return e
		}
	}
	require.Failf(t, "no field", "no field shows %q", text)
	return nil
}

/*
The form's widgets are what is read back.

The logic above is tested as numbers; this is the join between it and the
widgets, where a field built and never read would be a stat that looks editable
and is not.
*/
func TestTheEditorReadsWhatWasTyped(t *testing.T) {
	u := testUI(t)
	u.loadPrefixes()
	s := terraBlade()
	s.Defense, s.Pick, s.TileBoost = 3, 55, 4
	f := newItemFields(u, s)
	win := test.NewWindow(f.body)
	defer win.Close()

	now, err := f.read()
	require.NoError(t, err)
	require.Equal(t, formOf(s), now, "untouched, the form reads back as the slot")

	entryShowing(t, f.body, "85").SetText("500") // damage
	entryShowing(t, f.body, "3").SetText("7")    // defense
	entryShowing(t, f.body, "55").SetText("210") // pickaxe power
	entryShowing(t, f.body, "4").SetText("9")    // placement reach
	fynetest.FindCheck(f.body).SetChecked(false)
	fynetest.FindSelect(f.body).SetSelectedIndex(0)

	now, err = f.read()
	require.NoError(t, err)
	want := formOf(s)
	want.Damage, want.Defense, want.Pick, want.TileBoost = 500, 7, 210, 9
	want.AutoReuse, want.Prefix = false, 0
	require.Equal(t, want, now)

	entryShowing(t, f.body, "500").SetText("lots")
	_, err = f.read()
	require.Error(t, err, "a damage that is not a number is refused, not read as zero")
}

// While the Item field names something else, the stats on show are the old
// item's and would be ignored, so they are switched off rather than accepted.
func TestTheStatsAreOffWhileADifferentItemIsNamed(t *testing.T) {
	u := testUI(t)
	u.loadPrefixes()
	s := terraBlade()
	f := newItemFields(u, s)
	win := test.NewWindow(f.body)
	defer win.Close()

	damage := entryShowing(t, f.body, "85")
	require.False(t, damage.Disabled())

	item := entryShowing(t, f.body, "757")
	item.SetText("wood")
	require.True(t, damage.Disabled())
	require.True(t, fynetest.FindSelect(f.body).Disabled())
	require.Contains(t, fynetest.Texts(f.body), "Wood (#9)", "the form says what the name resolved to")

	now, err := f.read()
	require.NoError(t, err)
	require.Equal(t, 9, now.Type)

	item.SetText("757")
	require.False(t, damage.Disabled(), "naming the same item again gives the stats back")

	item.SetText("no such item anywhere")
	_, err = f.read()
	require.ErrorIs(t, err, errNoSuchItem)
}

/*
Right-clicking a cell opens its menu, and the sell list is on it.

The sell list used to be a button inside the editor, beside fields it had
nothing to do with. The menu is where it is now, and the editor has no such
button.
*/
func TestRightClickingACellOffersTheSellList(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{9: "Wood", 757: "Terra Blade"}
	u.fx.whitelist = []int{9}
	u.iv.slots[0] = client.ItemSlot{Slot: 0, Type: 757, Stack: 1}
	u.iv.slots[10] = client.ItemSlot{Slot: 10, Type: 9, Stack: 158}

	labels := func(m *fyne.Menu) []string {
		out := make([]string, 0, len(m.Items))
		for _, it := range m.Items {
			if !it.IsSeparator {
				out = append(out, it.Label)
			}
		}
		return out
	}
	require.Equal(t, []string{"Edit Terra Blade…", "Add to the sell list"}, labels(u.cellMenu(0)))
	require.Equal(t, []string{"Edit Wood…", "Take off the sell list"}, labels(u.cellMenu(10)))
	require.Equal(t, []string{"Place an item…"}, labels(u.cellMenu(20)),
		"an empty slot holds no kind of item to list")

	win := test.NewWindow(u.buildInventory())
	defer win.Close()
	win.Resize(fyne.NewSize(900, 700))

	buttons := fynetest.All[*slotButton](u.iv.cells[10])
	require.Len(t, buttons, 1, "a cell has one click target")
	require.Nil(t, win.Canvas().Overlays().Top())
	test.TapSecondary(buttons[0])
	menu := win.Canvas().Overlays().Top()
	require.NotNil(t, menu, "a right-click raises a menu")
	require.Contains(t, fynetest.Texts(menu), "Take off the sell list")

	f := newItemFields(u, u.iv.slots[10])
	require.NotContains(t, fynetest.Texts(f.body), "Auto-sell")
	require.Empty(t, fynetest.All[*widget.Button](f.body), "the editor has fields and no actions")
}

// Listing an item changes no slot, and a cell is redrawn only when its slot
// changes -- so the list arriving has to redraw the cells it re-marks itself.
func TestACellIsReMarkedWhenTheSellListChanges(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{9: "Wood", 757: "Terra Blade"}
	u.iv.slots[0] = client.ItemSlot{Slot: 0, Type: 757, Stack: 1}
	u.iv.slots[10] = client.ItemSlot{Slot: 10, Type: 9, Stack: 158}
	win := test.NewWindow(u.buildInventory())
	defer win.Close()

	marked := func(slot int) bool {
		tips := fynetest.Tips(u.iv.cells[slot])
		require.Len(t, tips, 1)
		return containsLine(tips[0], "Auto-sell: on")
	}
	require.False(t, marked(10))

	u.takeSellList([]int{757, 9})
	require.True(t, marked(10))
	require.True(t, marked(0))
	require.Equal(t, []int{9, 757}, u.fx.whitelist, "the list is kept in order")

	u.takeSellList([]int{757})
	require.False(t, marked(10), "taken off the list, the mark goes")
	require.True(t, marked(0))
}

func containsLine(text, line string) bool {
	for _, l := range splitLines(text) {
		if l == line {
			return true
		}
	}
	return false
}
