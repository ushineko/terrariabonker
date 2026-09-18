package gui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
projState is the projectile editor: which weapon is selected, what it fires, and
what has been asked of each projectile type.

Overrides are keyed by projectile, not by weapon. Two weapons can fire the same
projectile, and the section says so rather than pretending they are separate.
*/
type projState struct {
	overrides map[int]map[string]float64
	weapon    int         // the item type selected in the picker
	shoot     int         // the projectile that item fires, 0 for none
	known     map[int]int // item type -> what it fires, learned as weapons are picked
	says      string      // the line under the picker
}

// The editor's cadence and its picker's width.
//
// Fifty milliseconds because a projectile is rebuilt from the game's own
// literals whenever SetDefaults runs, which is once per shot: a slower sweep
// shows the unedited projectile for a frame or two before it catches up.
const (
	projEvery  = 50 * time.Millisecond
	weaponWide = 260
	projWide   = 170
)

// buildProjectiles changes what a weapon's projectiles do, while the game runs.
func (u *ui) buildProjectiles() fyne.CanvasObject {
	pick := widget.NewSelect(u.weaponNames(), nil)
	if name := u.itemName(u.pj.weapon); u.pj.weapon != 0 {
		pick.SetSelected(name)
	}
	pick.OnChanged = func(name string) { u.pickWeapon(name) }

	again := widget.NewButton("Re-read the inventory", func() { u.sh.Refresh() })
	head := container.NewHBox(
		widgets.Dim("Weapon"),
		widgets.WithTip(widgets.FixedWidth(pick, weaponWide),
			"The weapons in your inventory. Picking one looks up the projectile it fires."),
		again,
	)

	says := widgets.DimWrapped(widgets.OrNone(u.pj.says, "Pick a weapon from your inventory."))

	on := widget.NewCheck("Apply while I play", nil)
	on.SetChecked(u.projectiles.running())
	on.OnChanged = func(v bool) { u.setProjectiles(v) }
	u.sh.Gate(again)

	return container.NewBorder(
		container.NewVBox(
			widgets.Heading("Projectiles", "Change what a weapon's shots do."),
			head, says,
		),
		container.NewVBox(
			container.NewHBox(widgets.WithTip(on,
				"Re-applies the ticked fields to whatever is in flight, several times a "+
					"second, for as long as this is on.")),
			widgets.FixedHeight(u.logWidget(), logHeight),
		),
		nil, nil,
		container.NewVScroll(container.NewVBox(
			u.projFields(),
			widgets.DimWrapped("Nothing here is written into the game. A projectile is "+
				"rebuilt from the game's own numbers every time one is fired, so the edit "+
				"is re-applied while this runs and is gone when it stops."),
			widgets.DimWrapped("Lifetime is set once per projectile rather than held. A "+
				"shot that can never expire never frees its slot and the game only has "+
				"1001. Raising it is what lets a shot cross a thick wall: some projectiles "+
				"burn their life fast while inside solid blocks."),
		)),
	)
}

/*
projFields is the grid of editable fields.

A form layout, as the Patches section uses, so every value box in the column
starts at the same x whatever the label beside it happens to say.
*/
func (u *ui) projFields() fyne.CanvasObject {
	grid := container.New(layout.NewFormLayout())
	for _, f := range client.ProjectileFields {
		label, value := u.projRow(f)
		grid.Add(label)
		grid.Add(value)
	}
	return container.NewPadded(grid)
}

// projRow is one field: whether it is being written, and what to.
func (u *ui) projRow(f client.ProjectileField) (label, value fyne.CanvasObject) {
	saved, set := u.override(f.Name)

	on := widget.NewCheck(f.Label, nil)
	on.SetChecked(set)
	if u.pj.shoot == 0 {
		on.Disable()
	}

	if f.Kind == client.KindBool {
		// tileCollide is the checkbox itself: ticked means "pass through", which
		// is the field written as zero. A number box beside it would be a second
		// way of saying the same thing.
		on.OnChanged = func(v bool) { u.storeOverride(f, v, 0) }
		return widgets.WithTip(on, "Shots go through walls."), widget.NewLabel("")
	}

	box := widget.NewEntry()
	box.SetText(projText(f, saved, set))
	box.Validator = numberIn(f.Lo, f.Hi)
	if u.pj.shoot == 0 {
		box.Disable()
	}
	read := func() float64 {
		v, err := strconv.ParseFloat(box.Text, 64)
		if err != nil || v < f.Lo || v > f.Hi {
			return projDefault(f)
		}
		return v
	}
	box.OnChanged = func(string) {
		if on.Checked {
			u.storeOverride(f, true, read())
		}
	}
	on.OnChanged = func(v bool) { u.storeOverride(f, v, read()) }

	// The unit follows the box rather than being pinned beside it, for the same
	// reason the Patches grid does it: a long unit pinned in place sets a
	// minimum and not a maximum, and pushes the box left.
	value = container.NewHBox(widgets.FixedWidth(box, projWide), widgets.Dim(projUnit(f)))
	return widgets.WithTip(on, projNote(f)), value
}

// projNote is what a field does, in one statement.
func projNote(f client.ProjectileField) string {
	switch f.Name {
	case "penetrate":
		return "How many enemies one shot goes through. -1 never stops."
	case "extraUpdates":
		return "Extra movement steps per frame. A higher number is a faster shot."
	case "scale":
		return "How big the shot is drawn, and how big it hits."
	case "timeLeft":
		return "How long a shot lives, in ticks. 60 ticks is a second."
	}
	return f.Label
}

// projUnit names what the number beside a field is counted in.
func projUnit(f client.ProjectileField) string {
	switch f.Name {
	case "penetrate":
		return "enemies (-1 = no limit)"
	case "extraUpdates":
		return "steps"
	case "scale":
		return "x"
	case "timeLeft":
		return "ticks"
	}
	return ""
}

// projDefault is what a box holds before anyone has typed in it, from the Qt
// panel's own starting values.
func projDefault(f client.ProjectileField) float64 {
	switch f.Name {
	case "penetrate":
		return -1
	case "extraUpdates":
		return 2
	case "scale":
		return 2
	case "timeLeft":
		return 3000
	}
	return 0
}

// projText is a field's box contents: what was stored for this projectile, or
// the starting value when nothing has been.
func projText(f client.ProjectileField, saved float64, set bool) string {
	if !set {
		saved = projDefault(f)
	}
	return strconv.FormatFloat(saved, 'f', -1, 64)
}

// override reads what is stored for the selected projectile.
func (u *ui) override(name string) (value float64, set bool) {
	v, ok := u.pj.overrides[u.pj.shoot][name]
	return v, ok
}

/*
storeOverride records, or forgets, one field of the selected projectile.

Forgetting has to remove the projectile's whole entry when it was the last field:
an empty entry would keep the editor sweeping for a projectile nobody is editing.
*/
func (u *ui) storeOverride(f client.ProjectileField, on bool, value float64) {
	if u.pj.shoot == 0 {
		return
	}
	if u.pj.overrides == nil {
		u.pj.overrides = map[int]map[string]float64{}
	}
	if !on {
		delete(u.pj.overrides[u.pj.shoot], f.Name)
		if len(u.pj.overrides[u.pj.shoot]) == 0 {
			delete(u.pj.overrides, u.pj.shoot)
		}
		return
	}
	if u.pj.overrides[u.pj.shoot] == nil {
		u.pj.overrides[u.pj.shoot] = map[string]float64{}
	}
	u.pj.overrides[u.pj.shoot][f.Name] = value
}

// weaponNames is what the picker offers: one entry per item type held, by name.
func (u *ui) weaponNames() []string {
	seen := map[int]bool{}
	out := make([]string, 0, len(u.iv.slots))
	for _, s := range u.iv.slots {
		if s.Type == 0 || seen[s.Type] {
			continue
		}
		seen[s.Type] = true
		out = append(out, u.itemName(s.Type))
	}
	sort.Strings(out)
	return out
}

// pickWeapon resolves the chosen name back to an item and asks what it fires.
func (u *ui) pickWeapon(name string) {
	for _, s := range u.iv.slots {
		if s.Type != 0 && u.itemName(s.Type) == name {
			u.pj.weapon = s.Type
			u.askWhatItFires(s.Type)
			return
		}
	}
}

/*
askWhatItFires looks up Item.shoot and points the controls at it.

One step rather than two, because the two must not drift: setting the projectile
without reloading the boxes leaves the previous weapon's fields ticked, and the
next edit writes them onto the new projectile -- a leak between weapons that the
window gives no sign of.
*/
func (u *ui) askWhatItFires(itemType int) {
	u.sh.Load("Reading what it fires...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.ProjectileOfArgv(itemType))
		fyne.Do(func() {
			shoot, ok := 0, false
			for _, got := range client.Replies(out) {
				if v, is := got["shoot"].(float64); is {
					shoot, ok = int(v), true
				}
			}
			if !ok {
				u.pj.says = "Could not read what this fires: " + shortReason(out, err)
				u.sh.Refresh()
				return
			}
			if u.pj.known == nil {
				u.pj.known = map[int]int{}
			}
			u.pj.known[itemType] = shoot
			u.pj.shoot = shoot
			u.pj.says = u.firesLine(itemType, shoot)
			u.sh.Refresh()
		})
		return nil
	})
}

// firesLine says what the picked weapon fires, and who else fires it.
func (u *ui) firesLine(itemType, shoot int) string {
	if shoot == 0 {
		return u.itemName(itemType) + " fires no projectile, so there is nothing to change."
	}
	var shared []string
	for item, s := range u.pj.known {
		if item != itemType && s == shoot {
			shared = append(shared, u.itemName(item))
		}
	}
	sort.Strings(shared)
	line := fmt.Sprintf("Fires projectile %d.", shoot)
	if len(shared) > 0 {
		line += " Also fired by " + strings.Join(shared, ", ") + ". Editing one edits them all."
	}
	return line
}

// setProjectiles starts or stops the enforcement loop.
func (u *ui) setProjectiles(on bool) {
	if on && len(u.pj.overrides) == 0 {
		u.sh.Flash("Tick a field first. Nothing is being changed yet.", fd.StatusWarn)
		u.sh.Refresh()
		return
	}
	u.projectiles.set(on)
}

// readProjectiles handles one slice's reply: a refusal switches the cheat off
// rather than repeating itself fifty times a second.
func (u *ui) readProjectiles(raw string) {
	for _, got := range client.Replies(raw) {
		if msg, bad := got["error"].(string); bad && msg != "" {
			u.projectiles.say(msg)
			u.projectiles.set(false)
			u.sh.Refresh()
			return
		}
	}
}
