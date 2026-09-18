package gui

import (
	"context"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/logpane"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// Defaults for the three numbers this section sets, and the bounds the game
// itself enforces: 500 is the vanilla HP ceiling and 200 the mana one, but the
// trainer writes past both deliberately, so the spinner allows what the Qt
// window allowed.
const (
	defaultMaxHP   = 400
	minMaxHP       = 100
	maxMaxHP       = 9999
	defaultMaxMana = 200
	minMaxMana     = 20
	maxMaxMana     = 400
	defaultReach   = 20
	minReach       = 1
	maxReach       = 100
)

// logHeight is the output pane under the section. The Qt window capped its log
// box at 150 px; this is the same idea in the library's shape -- a fixed region
// written to in place, so a growing log never reflows the section above it.
const logHeight float32 = 160

/*
buildPlayer is the player themselves: what they are made of, and the two tools
that change how they interact with the world.

The layout follows the Qt window's Player tab -- Stats, then Tools -- because
this is a port and someone who knows the old window should not have to re-learn
where Heal is.
*/
func (u *ui) buildPlayer() fyne.CanvasObject {
	maxHP := u.spin(defaultMaxHP, minMaxHP, maxMaxHP)
	maxMana := u.spin(defaultMaxMana, minMaxMana, maxMaxMana)
	reach := u.spin(defaultReach, minReach, maxReach)

	heal := widget.NewButtonWithIcon("Heal to full", theme.MediaPlayIcon(), func() {
		u.do("Healing", client.SetHPArgv("max"))
	})
	refill := widget.NewButtonWithIcon("Refill mana", theme.MediaPlayIcon(), func() {
		u.do("Refilling mana", client.SetManaArgv("max"))
	})
	setHP := widget.NewButton("Set", func() {
		u.do("Setting max HP", client.SetMaxHPArgv(value(maxHP, defaultMaxHP)))
	})
	setMana := widget.NewButton("Set", func() {
		u.do("Setting max mana", client.SetMaxManaArgv(value(maxMana, defaultMaxMana)))
	})
	mining := widget.NewButton("Fast mining (all pickaxes)", func() {
		u.do("Setting pickaxe speed", client.FastMiningArgv())
	})
	longReach := widget.NewButton("Long reach", func() {
		u.do("Extending reach", client.LongReachArgv(value(reach, defaultReach)))
	})

	// Gated together: one operation at a time is the shell's rule, and a button
	// that stays live while the trainer is mid-write is a button that gets
	// pressed twice -- which for a memory write is not a harmless second press.
	u.sh.Gate(heal, refill, setHP, setMana, mining, longReach)

	stats := widgets.Card("Stats",
		container.NewHBox(heal, refill),
		widget.NewForm(
			widget.NewFormItem("Max HP", container.NewBorder(nil, nil, nil, setHP, maxHP)),
			widget.NewFormItem("Max mana", container.NewBorder(nil, nil, nil, setMana, maxMana)),
		),
		widgets.DimWrapped("Ceilings last until the game restarts."),
	)

	tools := widgets.Card("Tools",
		container.NewHBox(mining, widgets.Dim("reach +"), widgets.FixedWidth(reach, 90), longReach),
		widgets.DimWrapped("Applies to every pickaxe you carry. Reach is in tiles."),
	)

	return container.NewBorder(nil, widgets.FixedHeight(u.logWidget(), logHeight), nil, nil,
		container.NewVScroll(container.NewVBox(
			widgets.Heading("Player", "Set player stats and tool limits."),
			stats,
			tools,
		)))
}

// logWidget is the window's output, built once and re-used: the pane holds the
// lines, so a section rebuild must not throw them away.
func (u *ui) logWidget() fyne.CanvasObject {
	if u.log == nil {
		u.log = logpane.New(logpane.NewModel(logpane.DefaultMaxLines))
	}
	return u.log.Widget(logpane.Options{
		Title:     "Output",
		Height:    logHeight,
		Clipboard: u.sh.App.Clipboard(),
		Flash:     u.sh.Flash,
	})
}

// spin is a whole-number entry with bounds, the counterpart of the Qt window's
// QSpinBox. Fyne has no spin box, and a slider is wrong for a number someone
// wants to type exactly.
func (u *ui) spin(def, lo, hi int) *widget.Entry {
	return u.spinTo(def, lo, hi, nil)
}

// spinTo is spin that reports every valid value it is given. A loop reading the
// number needs it somewhere it can be read off the UI thread, so the setter puts
// it there as it is typed.
func (u *ui) spinTo(def, lo, hi int, set func(int)) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(strconv.Itoa(def))
	e.Validator = func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return errNotANumber
		}
		if n < lo || n > hi {
			return errOutOfRange(lo, hi)
		}
		return nil
	}
	if set != nil {
		e.OnChanged = func(s string) {
			if n, err := strconv.Atoi(s); err == nil && n >= lo && n <= hi {
				set(n)
			}
		}
	}
	return e
}

// value reads a spin entry, falling back to the default rather than refusing to
// act: the entry validates as it is typed, so a bad value is already visible.
func value(e *widget.Entry, def int) int {
	n, err := strconv.Atoi(e.Text)
	if err != nil {
		return def
	}
	return n
}

/*
do runs one operation and reports it.

Through the shell's Perform, so the window shows the busy popup after 300 ms,
refuses a second operation while one runs, and reports a failure as a banner
that stays until dismissed. The CLI's own output goes to the log, because for
this tool "what did it actually write" is the interesting part and a banner is
too small to hold it.
*/
func (u *ui) do(what string, argv []string) {
	u.sh.Perform(what+"...", func(ctx context.Context) error {
		out, err := u.run(ctx, argv)
		fyne.Do(func() {
			for _, line := range splitLines(out) {
				level := logpane.Info
				if err != nil {
					level = logpane.Warn
				}
				u.log.Log(level, line)
			}
			u.log.Draw()
			if err == nil {
				u.sh.Flash(what+": done.", fd.StatusGood)
			}
			// The numbers on screen are now stale whatever happened.
			u.loadStatus()
		})
		return err
	})
}
