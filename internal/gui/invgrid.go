package gui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
The inventory grid's layout and formatting, kept apart from the widgets that
draw it so it can be tested without a display.

This is the Go counterpart of terrariabonker/gui/invgrid.py, which is view code
and goes when the Qt panel does. Unlike the patch catalog, which lives in the
common layer and is read from the CLI, these are the window's own decisions
about how to draw a slot -- so they move across rather than being fetched.
*/

// gridSection is one titled block of the grid: its slots and how many columns
// they are laid out in. Slot 58 is internal and deliberately absent.
type gridSection struct {
	Title string
	From  int // inclusive
	To    int // exclusive
	Cols  int
}

// gridSections mirrors Terraria's own inventory layout.
var gridSections = []gridSection{
	{"Hotbar", 0, 10, 10},
	{"Inventory", 10, 50, 10},
	{"Coins", 50, 54, 4},
	{"Ammo", 54, 58, 4},
}

// rarityRGB is Terraria's canonical item-rarity name colours, the tooltip-name
// colour for each tier.
var rarityRGB = map[int][3]int{
	-13: {255, 60, 60},   // Master
	-12: {200, 160, 255}, // Expert (rainbow in-game; a static approximation)
	-11: {255, 175, 0},   // Quest / Amber
	-1:  {130, 130, 130}, // Gray (junk)
	0:   {200, 200, 200}, // White (common)
	1:   {150, 150, 255}, // Blue
	2:   {150, 255, 150}, // Green
	3:   {255, 200, 150}, // Orange
	4:   {255, 150, 150}, // Light red
	5:   {255, 150, 255}, // Pink
	6:   {210, 160, 255}, // Light purple
	7:   {150, 255, 10},  // Lime
	8:   {255, 255, 10},  // Yellow
	9:   {5, 200, 255},   // Cyan
	10:  {255, 40, 100},  // Red
	11:  {180, 40, 255},  // Purple
}

var defaultRarityRGB = [3]int{200, 200, 200}

// rarityName is Terraria's own name for each tier (the ItemRarityID constants).
var rarityName = map[int]string{
	-13: "Master", -12: "Expert", -11: "Quest", -1: "Gray",
	0: "White", 1: "Blue", 2: "Green", 3: "Orange", 4: "Light Red",
	5: "Pink", 6: "Light Purple", 7: "Lime", 8: "Yellow", 9: "Cyan",
	10: "Red", 11: "Purple",
}

// rarityColor is the bright colour for a tier.
func rarityColor(rare int) [3]int {
	if c, ok := rarityRGB[rare]; ok {
		return c
	}
	return defaultRarityRGB
}

// rarityLabel names a tier, or gives the bare number when it is one this does
// not know.
func rarityLabel(rare int) string {
	if n, ok := rarityName[rare]; ok {
		return n
	}
	return strconv.Itoa(rare)
}

/*
cellColors is the background and border for a slot, tinted by rarity.

The background is a dark tint so light cell text stays readable, and the border
is brighter. The arithmetic is the Qt panel's, kept exactly: the two windows
should tint a Legendary sword the same shade.
*/
func cellColors(rare int) (bg, border color.Color) {
	c := rarityColor(rare)
	dark := func(v int) uint8 { return uint8(v*20/100 + 20) }   //nolint:gosec // bounded by the table above
	bright := func(v int) uint8 { return uint8(v*55/100 + 45) } //nolint:gosec // same
	return color.NRGBA{R: dark(c[0]), G: dark(c[1]), B: dark(c[2]), A: 255},
		color.NRGBA{R: bright(c[0]), G: bright(c[1]), B: bright(c[2]), A: 255}
}

// stackBadge is the count drawn in the corner of a cell, shown only when a slot
// holds more than one.
func stackBadge(stack int) string {
	if stack > 1 {
		return strconv.Itoa(stack)
	}
	return ""
}

/*
abbrev shortens an item name to fit a cell, for the case where the sprite cache
has no icon for it.

Short names pass through. A multi-word name collapses to dot-joined prefixes
("Copper Pickaxe" becomes "Cop.Pick"); a long single word is truncated.
*/
func abbrev(name string, width int) string {
	name = strings.TrimSpace(name)
	if len([]rune(name)) <= width {
		return name
	}
	if words := strings.Fields(name); len(words) >= 2 {
		take := (width - (len(words) - 1)) / len(words)
		if take < 2 {
			take = 2
		}
		parts := make([]string, 0, len(words))
		for _, w := range words {
			r := []rune(w)
			if len(r) > take {
				r = r[:take]
			}
			parts = append(parts, string(r))
		}
		if short := strings.Join(parts, "."); len([]rune(short)) <= width+3 {
			return short
		}
	}
	r := []rune(name)
	return string(r[:width-1]) + "…"
}

/*
cellTip is the hover text for a slot: everything about the item that the cell
itself has no room to say.

The same detail the Qt panel's tooltip carries, because it is the only place a
player can see what a slot actually holds -- damage, modifier, pickaxe power --
without opening the editor.
*/
func cellTip(s client.ItemSlot, name string, listed bool) string {
	if s.Empty() {
		return fmt.Sprintf("Slot %d, empty. Click to place an item.", s.Slot)
	}
	lines := []string{
		fmt.Sprintf("%s  (#%d)", name, s.Type),
		fmt.Sprintf("Slot %d", s.Slot),
		fmt.Sprintf("Stack %d", s.Stack),
	}
	if s.Damage >= 0 {
		lines = append(lines, fmt.Sprintf("Damage %d", s.Damage))
	}
	if s.Defense > 0 {
		lines = append(lines, fmt.Sprintf("Defense %d", s.Defense))
	}
	if s.Pick > 0 {
		lines = append(lines, fmt.Sprintf("Pickaxe power %d%%", s.Pick))
	}
	if s.TileBoost > 0 {
		lines = append(lines, fmt.Sprintf("Placement reach +%d", s.TileBoost))
	}
	lines = append(lines, fmt.Sprintf("Rarity: %s (%d)", rarityLabel(s.Rare), s.Rare))
	if s.AutoReuse != 0 {
		lines = append(lines, "Auto-reuse on")
	} else {
		lines = append(lines, "Auto-reuse off")
	}
	lines = append(lines, fmt.Sprintf("Use time %d", s.UseTime))
	if listed {
		lines = append(lines, "Auto-sell: on")
	}
	return strings.Join(lines, "\n")
}

/*
sameSlot reports whether two readings of a slot would draw the same cell.

Compared field by field because ItemSlot carries a map and Go will not compare
those, and comparing them field by field is the honest version anyway: every
field the cell or its tip shows is named here, so adding one to the tip without
adding it here would leave a cell that never updates for it.

Flags are left out on purpose. They are the damage-class booleans, which decide
which modifiers an item may take -- the editor's business, not the cell's.
*/
func sameSlot(a, b client.ItemSlot) bool {
	return a.Slot == b.Slot &&
		a.Type == b.Type &&
		a.Stack == b.Stack &&
		a.Damage == b.Damage &&
		a.AutoReuse == b.AutoReuse &&
		a.UseTime == b.UseTime &&
		a.Pick == b.Pick &&
		a.TileBoost == b.TileBoost &&
		a.UseAnim == b.UseAnim &&
		a.Rare == b.Rare &&
		a.Defense == b.Defense &&
		a.Prefix == b.Prefix
}
