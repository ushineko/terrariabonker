package gui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// The grid is Terraria's own layout. Slot 58 is internal and is deliberately
// not drawn, so the sections must cover 0..57 and stop.
func TestTheGridCoversEverySlotTheGameShows(t *testing.T) {
	seen := map[int]string{}
	for _, sec := range gridSections {
		for slot := sec.From; slot < sec.To; slot++ {
			require.NotContainsf(t, seen, slot, "slot %d is in two sections", slot)
			seen[slot] = sec.Title
		}
	}
	require.Len(t, seen, 58, "slots 0..57, and not 58, which is internal")
	require.Equal(t, "Hotbar", seen[0])
	require.Equal(t, "Inventory", seen[10])
	require.Equal(t, "Coins", seen[50])
	require.Equal(t, "Ammo", seen[57])
	require.NotContains(t, seen, 58)
}

/*
The rarity tint must match the Qt panel's exactly.

Both windows exist at once during the port, and a Legendary sword that is one
shade in one and another shade in the other is a difference someone has to stop
and work out. The arithmetic is copied; this pins the result.
*/
func TestRarityTintsMatchTheQtPanel(t *testing.T) {
	// Blue (tier 1) is (150, 150, 255): bg = v*20/100+20, border = v*55/100+45.
	bg, border := cellColors(1)
	require.Equal(t, color.NRGBA{R: 50, G: 50, B: 71, A: 255}, bg)
	require.Equal(t, color.NRGBA{R: 127, G: 127, B: 185, A: 255}, border)

	// An unknown tier falls back to white rather than to black, which would
	// read as an empty slot.
	unknown, _ := cellColors(999)
	white, _ := cellColors(0)
	require.Equal(t, white, unknown)
}

// A stack badge is for a stack, not for a single thing: every filled slot would
// otherwise carry a "1" that says nothing.
func TestTheStackBadgeOnlyCountsWhatIsWorthCounting(t *testing.T) {
	require.Equal(t, "", stackBadge(0))
	require.Equal(t, "", stackBadge(1))
	require.Equal(t, "2", stackBadge(2))
	require.Equal(t, "999", stackBadge(999))
}

/*
The fallback label shortens a name the way the Qt panel did.

These expectations were taken from the Python while both existed, not written
from the implementation: the test that produced them asked `gui/invgrid.py` for
each answer, and that is how the Qt panel's own docstring was found to be wrong.
It says "Copper Pickaxe" becomes "Cop.Pick"; the code it documented produced
"Cop.Pic", and so does this.

The Python is gone with the panel (spec 051), so the differential check goes with
it and what it established stays.
*/
func TestNamesAreShortenedTheWayTheQtPanelDidIt(t *testing.T) {
	for name, want := range map[string]string{
		"Wood":                  "Wood",
		"Copper Pickaxe":        "Cop.Pic",
		"Meteoritebar":          "Meteori…",
		"Molten Pickaxe":        "Mol.Pic",
		"Terra Blade":           "Ter.Bla",
		"Chlorophyte Warhammer": "Chl.War",
		"Zenith":                "Zenith",
	} {
		require.Equalf(t, want, abbrev(name, abbrevWidth), "%q is shortened differently", name)
	}
}

// The tip is where a slot's detail lives, because the cell has room for an icon
// and a number and nothing else.
func TestTheCellTipCarriesWhatTheCellCannotShow(t *testing.T) {
	s := client.ItemSlot{
		Slot: 3, Type: 29, Stack: 7, Damage: 50, Defense: 2, Pick: 100,
		TileBoost: 5, Rare: 1, AutoReuse: 1, UseTime: 12, Prefix: 27,
	}
	tip := cellTip(s, "Fabled Copper Pickaxe", true)
	for _, want := range []string{
		"Fabled Copper Pickaxe  (#29)", "Slot 3", "Stack 7", "Damage 50",
		"Defense 2", "Pickaxe power 100%", "Placement reach +5",
		"Rarity: Blue (1)", "Auto-reuse on", "Use time 12", "Auto-sell: on",
	} {
		require.Containsf(t, tip, want, "%q is missing from the tip", want)
	}

	empty := cellTip(client.ItemSlot{Slot: 12}, "", false)
	require.Contains(t, empty, "Slot 12, empty")
	require.Contains(t, empty, "Click to place an item.")
}

/*
A cell is redrawn only when its slot actually changed.

The grid re-reads once a second. Redrawing an unchanged cell resets its icon and
takes down a tip somebody is reading, so this is a correctness rule about the
interface and not only a saving.
*/
func TestOnlyAChangedSlotCountsAsChanged(t *testing.T) {
	a := client.ItemSlot{Slot: 3, Type: 29, Stack: 7, Rare: 1, Flags: map[string]any{"melee": true}}
	b := a
	b.Flags = map[string]any{"melee": true, "ranged": false}
	require.True(t, sameSlot(a, b), "flags decide which modifiers an item takes, not how a cell looks")

	b = a
	b.Stack = 8
	require.False(t, sameSlot(a, b), "a stack that moved must redraw the badge")

	b = a
	b.Prefix = 27
	require.False(t, sameSlot(a, b), "a modifier changes the name in the tip")

	b = a
	b.UseTime = 99
	require.False(t, sameSlot(a, b), "the tip shows use time, so it must notice one")
}

// An empty slot is type 0, which is the game's own way of saying so.
func TestAnEmptySlotIsTypeZero(t *testing.T) {
	require.True(t, client.ItemSlot{Slot: 4}.Empty())
	require.False(t, client.ItemSlot{Slot: 4, Type: 29}.Empty())
}

/*
The whole section must render to an image, not only to a window.

Rendering headlessly is how a layout gets checked without a display, a
compositor or anybody to look at it -- and it catches the class of bug that is
invisible in front of a person, where a nil colour draws as nothing under the
GL painter and crashes the software one.
*/
func TestTheInventorySectionRendersToAnImage(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{9: "Wood", 757: "Terra Blade"}
	u.iv.prefix = map[int]client.Prefix{27: {ID: 27, Name: "Adept", Quality: "good"}}
	u.fx.whitelist = []int{9}
	for _, s := range []client.ItemSlot{
		{Slot: 0, Type: 757, Stack: 1, Rare: 8, Damage: 90, UseTime: 12, Prefix: 27},
		{Slot: 10, Type: 9, Stack: 158},
	} {
		u.iv.slots[s.Slot] = s
	}

	win := test.NewWindow(u.buildInventory())
	defer win.Close()
	win.Resize(fyne.NewSize(900, 700))

	require.NotPanics(t, func() { _ = win.Canvas().Capture() })

	texts := fynetest.Texts(win.Canvas().Content())
	require.Contains(t, texts, "Hotbar")
	require.Contains(t, texts, "Ammo")
	require.Contains(t, texts, "158", "a stack of 158 shows its count")
}
