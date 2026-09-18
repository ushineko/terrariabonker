package gui

import (
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/ushineko/fynedesygn/dialogs"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// How often each loop sends a round. These are the Qt panel's, and each is
// chosen against what its round writes rather than picked for tidiness: a buff
// renewed slower than it lasts visibly flickers, and a catch noticed a second
// late is a fish already gone.
const (
	potionEvery  = 250 * time.Millisecond
	fishingEvery = time.Second
	buffEvery    = time.Second
	catchEvery   = 50 * time.Millisecond
	sellEvery    = 500 * time.Millisecond
)

// Defaults and bounds for the numbers on this section.
const (
	defaultBait  = 30
	minBait      = 1
	maxBait      = 999
	defaultPower = 255
	minPower     = 1
	maxPower     = 255
	defaultStack = 1
	minStack     = 1
	maxStack     = 999
)

/*
buildEffects is the cheats the trainer holds up while it is open.

Separate from Patches because they differ in the way a player notices: close the
window and these stop, while a patch keeps working until the game restarts.
*/
func (u *ui) buildEffects() fyne.CanvasObject {
	return u.logSplit(
		container.NewVScroll(container.NewVBox(
			widgets.Heading("Effects", "Cheats that run while the trainer is open."),
			u.freezeCard(),
			u.fishingCard(),
			u.buffCard(),
			u.potionCard(),
			u.sellCard(),
		)))
}

// freezeCard pins HP and mana against the game.
func (u *ui) freezeCard() fyne.CanvasObject {
	god := widget.NewCheck("Godmode (pin HP)", nil)
	mana := widget.NewCheck("Infinite mana", nil)
	god.SetChecked(u.fx.god)
	mana.SetChecked(u.fx.mana)

	apply := func(bool) {
		u.fx.god, u.fx.mana = god.Checked, mana.Checked
		u.freeze.set(u.fx.god, u.fx.mana)
		u.sh.RedrawStatus()
	}
	god.OnChanged, mana.OnChanged = apply, apply

	return widgets.Card("Freezes",
		container.NewHBox(
			widgets.WithTip(god, "Pins HP to maximum. Held by a loop of its own, so it "+
				"stops when the trainer closes."),
			widgets.WithTip(mana, "Pins mana to maximum."),
		),
	)
}

/*
fishingCard is the rod, the bait and the reeling.

Rod power is the one thing on this section written into the save, so switching
the watch off restores what each rod had. Everything else here evaporates when
the window closes.
*/
func (u *ui) fishingCard() fyne.CanvasObject {
	// Typed values are pushed into fx, because the watch reads them there once a
	// second. Reading the widget from the loop instead would touch a widget off
	// the UI thread.
	bait := u.spinTo(u.fx.bait, minBait, maxBait, func(v int) { u.fx.bait = v })
	power := u.spinTo(u.fx.power, minPower, maxPower, func(v int) { u.fx.power = v })

	kit := widget.NewCheck("Rod and bait, and bait that does not run out", nil)
	catch := widget.NewCheck("Reel in for me", nil)
	recast := widget.NewCheck("and cast", nil)
	recast.Disable()

	kit.SetChecked(u.fishing.running())
	catch.SetChecked(u.catch.running())
	recast.SetChecked(u.fx.recast)
	if u.catch.running() {
		recast.Enable()
	}

	kit.OnChanged = func(on bool) {
		if on {
			// The rods are raised once as the watch starts, not every round.
			u.fx.kitDone = false
			u.once("Raising rod power", client.FishingPowerArgv(u.fx.power))
		}
		u.fishing.set(on)
		if !on {
			u.once("Restoring rod power", client.FishingRestoreArgv())
		}
	}
	recast.OnChanged = func(on bool) { u.fx.recast = on }
	catch.OnChanged = func(on bool) {
		if on {
			recast.Enable()
		} else {
			recast.SetChecked(false)
			recast.Disable()
		}
		u.catch.set(on)
	}

	// One row, as the Qt panel has it. What each control does is on hover.
	return widgets.Card("Fishing",
		container.NewHBox(
			widgets.WithTip(kit, "Hands you a rod and bait if you have none, and tops any bait "+
				"stack back up as you fish. Your own gear is left alone. Water under 300 tiles "+
				"cuts fishing power, so fish in a lake."),
			widgets.WithTip(widgets.Dim("keep bait at"), "Any bait stack below this is topped "+
				"back up to it."),
			widgets.FixedWidth(bait, 90),
			widgets.WithTip(widgets.Dim("rod power"), "Every rod you carry is raised to this "+
				"while the cheat is on, and put back when you switch it off. High power also "+
				"makes fish bite quickly."),
			widgets.FixedWidth(power, 90),
		),
		container.NewHBox(
			widgets.WithTip(catch, "Takes every fish that bites, one press per bite. You still "+
				"cast. Needs Auto-use on the Patches section, which is what presses the button."),
			widgets.WithTip(recast, "Casts again after each catch. It waits until you have cast "+
				"once yourself, and stops when you untick Reel in for me."),
		),
	)
}

// buffCard holds the fishing potion effects up without the potions.
func (u *ui) buffCard() fyne.CanvasObject {
	power := widget.NewCheck("Fishing power", nil)
	sonar := widget.NewCheck("Sonar", nil)
	crate := widget.NewCheck("Crates", nil)

	apply := func(bool) {
		u.fx.buffPower, u.fx.buffSonar, u.fx.buffCrate = power.Checked, sonar.Checked, crate.Checked
		u.buffs.set(power.Checked || sonar.Checked || crate.Checked)
	}
	power.OnChanged, sonar.OnChanged, crate.OnChanged = apply, apply, apply
	power.SetChecked(u.fx.buffPower)
	sonar.SetChecked(u.fx.buffSonar)
	crate.SetChecked(u.fx.buffCrate)

	return widgets.Card("Fishing potion effects",
		container.NewHBox(
			widgets.WithTip(power, "A Fishing Potion's effect without the potion: +15 fishing "+
				"power while it is ticked."),
			widgets.WithTip(sonar, "A Sonar Potion's effect without the potion: what is biting "+
				"is named before you reel it in."),
			widgets.WithTip(crate, "A Crate Potion's effect without the potion: crates come up "+
				"more often."),
		),
	)
}

// potionCard keeps favorited potions working from the bag.
func (u *ui) potionCard() fyne.CanvasObject {
	stack := u.spinTo(u.fx.stack, minStack, maxStack, func(v int) { u.fx.stack = v })
	on := widget.NewCheck("Favorited potions work from the inventory", nil)
	on.SetChecked(u.potions.running())
	on.OnChanged = func(v bool) {
		u.potions.set(v)
		if !v {
			u.note("[potions] off, your buffs will lapse on their own")
		}
	}

	return widgets.Card("Passive potions",
		container.NewHBox(
			widgets.WithTip(on, "Alt-click a potion to favorite it and its effect stays up "+
				"while it sits in your bag. The potion is not used and the stack does not "+
				"shrink. Only favorited potions count."),
			widgets.WithTip(widgets.Dim("min stack"), "Only potions with at least this many in "+
				"the stack take effect."),
			widgets.FixedWidth(stack, 90),
		),
	)
}

/*
sellCard sells whitelisted items as they arrive.

The list opens in a dialog rather than sitting under the switch. It is the one
thing on this section that grows without bound -- a player can whitelist as many
items as they like -- and a pane pinned to a fixed height at the bottom of a card
gives a list of twenty items four rows and a scrollbar while the section above it
has room to spare.
*/
func (u *ui) sellCard() fyne.CanvasObject {
	on := widget.NewCheck("Sell whitelisted items as they arrive", nil)
	on.SetChecked(u.sell.running())
	on.OnChanged = func(v bool) { u.sell.set(v) }

	open := widget.NewButton(sellListLabel(len(u.fx.whitelist)), func() { u.showSellList() })

	return widgets.Card("Auto-sell",
		widgets.WithTip(on, "Anything on the list is sold for coins as it arrives. The "+
			"coins go to your piggy bank if you can reach one. Favorited stacks are never "+
			"sold. Selling is permanent once your world saves."),
		widgets.WithTip(open, "Add an item by right-clicking it in the Inventory section."),
	)
}

// sellListLabel says how much is on the list, so the button is worth reading
// before it is pressed.
func sellListLabel(n int) string {
	switch n {
	case 0:
		return "The sell list is empty"
	case 1:
		return "The sell list: 1 item"
	}
	return fmt.Sprintf("The sell list: %d items", n)
}

/*
showSellList opens the whitelist.

A dialog because the list is read and edited in bursts -- whitelist a few things,
then forget about it -- and because it is the one list here with no natural
length. Items are added by right-clicking one in the Inventory grid; a
whitelisted item is sold on the next round, so it never stays in the grid long
enough to right-click a second time, which is why taking one off has to be
possible here.
*/
func (u *ui) showSellList() {
	list := widget.NewList(
		func() int { return len(u.fx.whitelist) },
		func() fyne.CanvasObject {
			return container.NewHBox(u.itemIcon(0, rowIconSize), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row, ok := o.(*fyne.Container)
			if !ok || len(row.Objects) != 2 || i < 0 || i >= len(u.fx.whitelist) {
				return
			}
			icon, _ := row.Objects[0].(*canvas.Image)
			label, _ := row.Objects[1].(*widget.Label)
			if icon == nil || label == nil {
				return
			}
			itemType := u.fx.whitelist[i]
			icon.Resource = u.sprites.icon(itemType)
			icon.Refresh()
			label.SetText(fmt.Sprintf("%s (#%d)", u.itemName(itemType), itemType))
		},
	)
	list.OnSelected = func(i widget.ListItemID) { u.fx.sellPick = i }
	u.fx.sellList = list

	remove := widget.NewButton("Remove selected", func() { u.removeFromSellList() })
	body := container.NewBorder(nil, remove, nil, nil, list)

	dialogs.ShowDetail(u.sh.Window, "The sell list", body, sellDialogW, sellDialogH)
}

// removeFromSellList takes the picked item off, and says so when nothing is
// picked rather than doing nothing.
func (u *ui) removeFromSellList() {
	i := u.fx.sellPick
	if i < 0 || i >= len(u.fx.whitelist) {
		u.note("[sell] pick a row to remove")
		return
	}
	itemType := u.fx.whitelist[i]
	u.once("Removing from the sell list", client.SellListArgv(nil, &itemType))
	u.loadSellList()
}

// The sell list's dialog: tall enough for a dozen rows without scrolling, which
// is more than most lists ever hold.
const (
	sellDialogW float32 = 480
	sellDialogH float32 = 520
)

// startWatches describes every loop this section drives. Built once, with the
// window, so a watch survives the section being rebuilt.
func (u *ui) startWatches() {
	u.freeze = &freezer{u: u}

	u.potions = newWatch(u, "potions", potionEvery,
		func() []string { return client.PotionsArgv(u.fx.stack) },
		func(raw string) { u.readPotions(raw) })

	u.fishing = newWatch(u, "fishing", fishingEvery,
		func() []string { return client.FishingArgv(u.fx.bait, !u.fx.kitDone) },
		func(raw string) { u.readFishing(raw) })

	u.buffs = newWatch(u, "buffs", buffEvery,
		func() []string {
			return client.FishingBuffsArgv(u.fx.buffPower, u.fx.buffSonar, u.fx.buffCrate)
		},
		func(raw string) { u.readBuffs(raw) })

	u.catch = newWatch(u, "catch", catchEvery,
		func() []string { return client.CatchArgv(u.fx.recast) },
		func(raw string) { u.readCatch(raw) }).
		onStop(client.CatchStopArgv)

	u.sell = newWatch(u, "sell", sellEvery,
		client.SellTickArgv,
		func(raw string) { u.readSell(raw) })

	u.projectiles = newWatch(u, "projectile", projEvery,
		func() []string { return client.ProjectileTickArgv(u.pj.overrides) },
		func(raw string) { u.readProjectiles(raw) }).
		onStop(client.ProjectileStopArgv)
}

// readPotions reports the buffs that came up, and the ones that had nowhere to go.
func (u *ui) readPotions(raw string) {
	for _, got := range client.Replies(raw) {
		for _, e := range rows(got, "added") {
			u.potions.sayOnce(fmt.Sprintf("buff:%v", e["buff"]),
				fmt.Sprintf("slot %v buff %v is up", e["slot"], e["buff"]))
		}
		for _, e := range rows(got, "full") {
			u.potions.sayOnce(fmt.Sprintf("full:%v", e["buff"]),
				fmt.Sprintf("no free buff slot for buff %v, unfavorite something or let one expire", e["buff"]))
		}
	}
}

// readFishing reports the kit handed out and the stacks topped up.
func (u *ui) readFishing(raw string) {
	for _, got := range client.Replies(raw) {
		if kit, ok := got["kit"].(map[string]any); ok {
			if gave, ok := kit["gave"].(map[string]any); ok {
				for what, e := range gave {
					slot := ""
					if m, ok := e.(map[string]any); ok {
						slot = fmt.Sprintf(" (slot %v)", m["slot"])
					}
					u.fishing.say("gave you a " + what + slot)
				}
			}
		}
		u.fx.kitDone = true
		if bait, ok := got["bait"].(map[string]any); ok {
			for _, e := range toRows(bait["topped"]) {
				// Once per stack. Every bait consumed is a top-up, so a line each
				// buries the output within a minute of fishing.
				u.fishing.sayOnce(fmt.Sprintf("bait:%v", e["slot"]),
					fmt.Sprintf("topping up the bait in slot %v", e["slot"]))
			}
		}
	}
}

// readBuffs reports what the round deferred, once.
func (u *ui) readBuffs(raw string) {
	for _, got := range client.Replies(raw) {
		for _, e := range rows(got, "deferred") {
			u.buffs.sayOnce(fmt.Sprintf("deferred:%v", e["buff"]),
				fmt.Sprintf("buff %v is waiting for a free slot", e["buff"]))
		}
	}
}

// readCatch reports each fish taken. Rare enough to say every time.
func (u *ui) readCatch(raw string) {
	for _, got := range client.Replies(raw) {
		if n, ok := got["caught"].(float64); ok && n > 0 {
			u.catch.say(fmt.Sprintf("reeled in %d", int(n)))
		}
	}
}

// readSell reports what was sold and where the coins went.
func (u *ui) readSell(raw string) {
	for _, got := range client.Replies(raw) {
		for _, e := range rows(got, "sold") {
			u.sell.say(fmt.Sprintf("sold %v x%v for %v", e["name"], e["stack"], e["value"]))
		}
	}
}

// loadSellList refreshes the whitelist from the profile, which is its owner.
func (u *ui) loadSellList() {
	go func() {
		out, err := u.work.Do(client.SellListArgv(nil, nil))
		if err != nil {
			return
		}
		list, ok := client.ParseSellList(out)
		if !ok {
			return
		}
		sort.Ints(list)
		fyne.Do(func() {
			u.fx.whitelist = list
			if u.fx.sellList != nil {
				u.fx.sellList.Refresh()
			}
		})
	}()
}

// rows reads a named list of objects out of a reply.
func rows(got map[string]any, key string) []map[string]any { return toRows(got[key]) }

func toRows(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
