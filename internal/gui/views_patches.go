package gui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
buildPatches is what has been written into the running game.

Patches differ from the Effects section in the way a player notices: a patch
keeps working after the trainer closes, until the game restarts.

Grouped by the catalog's own sections rather than listed flat. The flat list
needed scrolling at twelve patches and the catalog only grows.
*/
func (u *ui) buildPatches() fyne.CanvasObject {
	head := widgets.Heading("Patches", "Code written into the running game.")

	if len(u.px.catalog) == 0 {
		return container.NewBorder(nil, widgets.FixedHeight(u.logWidget(), logHeight), nil, nil,
			container.NewVScroll(container.NewVBox(head, widgets.Card("Catalog",
				widgets.Wrapped("The patch catalog has not been read yet."),
				widgets.DimWrapped("It comes from the CLI. If this stays empty, "+cliName+
					" is not on PATH or it failed to run."),
			))))
	}

	// A tab per section, as the Qt panel has. One flat list needed scrolling at
	// twelve patches and the catalog only grows; tabs keep the section a fixed
	// height however many are added.
	tabs := container.NewAppTabs()
	for _, section := range u.px.sections {
		tabs.Append(container.NewTabItem(section, u.patchGrid(section)))
	}

	return container.NewBorder(
		head,
		container.NewVBox(u.restoreCard(), widgets.FixedHeight(u.logWidget(), logHeight)),
		nil, nil, tabs)
}

/*
patchGrid is one of the catalog's sections, laid out as a grid.

A form layout rather than a row per patch. It is two columns and it sizes the
first to the widest thing in it, so every value control in the section starts at
the same x. Rows built independently cannot do that: each one is only as wide as
its own contents, and the boxes step in and out down the section according to how
long each label and unit happens to be.

What each patch does is a hover tip rather than a line under it. Printed under
every row it tripled the height of the section and buried the controls.
*/
func (u *ui) patchGrid(section string) fyne.CanvasObject {
	grid := container.New(layout.NewFormLayout())
	for _, p := range u.px.catalog {
		if p.Section != section {
			continue
		}
		label, value := u.patchRow(p)
		grid.Add(label)
		grid.Add(value)
	}
	return container.NewVScroll(container.NewPadded(grid))
}

/*
patchRow is one patch: a switch, the number it takes, and what it does.

The switch is set from the game's own state rather than from what was last
clicked. A patch is in the running process, so a game restart clears it while
the window still has the box ticked, and the status poll is what notices.
*/
func (u *ui) patchRow(p client.Patch) (label, value fyne.CanvasObject) {
	on := widget.NewCheck(p.Label, nil)
	on.SetChecked(u.px.on[p.Name])
	why, usable := u.patchStanding(p)
	if !usable {
		on.Disable()
	}

	var read func() *float64

	switch {
	case p.Value == nil:
		read = func() *float64 { return nil }
		value = widget.NewLabel("")
	case len(p.Value.Presets) > 0:
		labels := make([]string, 0, len(p.Value.Presets))
		byLabel := map[string]float64{}
		for _, pr := range p.Value.Presets {
			labels = append(labels, pr.Label)
			byLabel[pr.Label] = pr.Value
		}
		sel := widget.NewSelect(labels, nil)
		sel.SetSelected(presetFor(p, u.px.values[p.Name]))
		sel.OnChanged = func(string) {
			if on.Checked {
				u.setPatch(p, true, read())
			}
		}
		// Boxed the same way as a typed value. The grid's second column
		// expands, and a Select left to fill it would stretch across the
		// window while the entries beside it stayed 170 wide.
		value = container.NewHBox(widgets.FixedWidth(sel, patchValueWidth))
		read = func() *float64 {
			v, ok := byLabel[sel.Selected]
			if !ok {
				return nil
			}
			return &v
		}
	default:
		entry := widget.NewEntry()
		entry.SetText(formatValue(p, u.px.values[p.Name]))
		entry.Validator = numberIn(p.Value.Lo, p.Value.Hi)
		// The unit follows the box rather than being pinned beside it. Pinning
		// it set a minimum and not a maximum, so a long unit overflowed and
		// pushed the box left, which is what made the column ragged.
		value = container.NewHBox(
			widgets.FixedWidth(entry, patchValueWidth), widgets.Dim(p.Value.Unit))
		read = func() *float64 {
			v, err := strconv.ParseFloat(entry.Text, 64)
			if err != nil || v < p.Value.Lo || v > p.Value.Hi {
				return nil
			}
			return &v
		}
	}

	on.OnChanged = func(v bool) { u.setPatch(p, v, read()) }
	return widgets.WithTip(on, why), value
}

/*
patchStanding is what a patch's control should say, and whether it should work.

Three states the game can put a patch in, and they are not the same thing:
switched off by the build gate, unavailable on this build, or resolving but
unproven. The tip carries the reason, because the row has room for a label and a
number and nothing else.
*/
func (u *ui) patchStanding(p client.Patch) (why string, usable bool) {
	if u.gated(p.Name) {
		return "Off for this build. It did not match when the game updated and you " +
			"chose to carry on without it. Re-check with `" + cliName + " build-check`.", false
	}
	d, known := u.px.detail[p.Name]
	switch {
	case !known:
		return p.Note, true
	case !d.Available:
		return "Not available on this build: " + widgets.OrNone(d.Reason, "the pattern does "+
			"not resolve here") + ".", false
	case !d.Verified:
		return p.Note + "\n\nThis one resolves on the running build but was confirmed on " +
			"a different one.", true
	}
	return p.Note, true
}

// patchValueWidth is the width of a value box. The grid aligns where they
// start; this makes them the same size as each other.
const patchValueWidth float32 = 170

// setPatch writes one patch and then re-reads the game, because what a patch
// did is reported by the status rather than by the command.
func (u *ui) setPatch(p client.Patch, on bool, value *float64) {
	what := "Disabling " + p.Name
	if on {
		what = "Enabling " + p.Name
	}
	u.sh.Perform(what+"...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.PatchSetArgv(p.Name, on, value))
		fyne.Do(func() {
			for _, line := range splitLines(out) {
				u.note(line)
			}
			if err == nil {
				u.loadPatches()
			}
		})
		return err
	})
}

/*
restoreCard puts the saved patches back.

A patch lives in the running process, so restarting the game or loading another
world clears every one of them while the profile still says they should be on.
This is what puts them back.
*/
func (u *ui) restoreCard() fyne.CanvasObject {
	run := widget.NewButton("Restore saved patches", func() {
		u.sh.Perform("Restoring...", func(ctx context.Context) error {
			out, err := u.run(ctx, client.RestoreArgv())
			fyne.Do(func() {
				for _, line := range splitLines(out) {
					u.note(line)
				}
				if err == nil {
					u.sh.Flash("Restored.", fd.StatusGood)
					u.loadPatches()
				}
			})
			return err
		})
	})
	u.sh.Gate(run)

	build := widgets.OrNone(u.px.build, "unknown")

	// One row. This sits under the tabs and every pixel it takes is one the
	// patch list does not get.
	return container.NewHBox(
		widgets.WithTip(run, "A game restart clears every patch. This puts back what the "+
			"profile says should be on."),
		widgets.Dim("build"), widget.NewLabel(build), u.patchVerdict(),
	)
}

/*
patchVerdict is how the whole catalog stands on the running build.

A count rather than a yes or no. "Unverified" was the only thing the Qt panel
said for a long time and it covers two very different situations -- every cheat
working but unproven, and four of twelve not resolving at all -- which is the
gap the build gate was written to close.
*/
func (u *ui) patchVerdict() fyne.CanvasObject {
	if len(u.px.catalog) == 0 {
		return widgets.StatusText("not read", fd.StatusInfo)
	}
	var dead, unproven []string
	for name, d := range u.px.detail {
		switch {
		case !d.Available:
			dead = append(dead, name)
		case !d.Verified:
			unproven = append(unproven, name)
		}
	}
	sort.Strings(dead)

	if len(dead) > 0 {
		return widgets.WithTip(
			widgets.StatusText(fmt.Sprintf("%d of %d do not resolve here",
				len(dead), len(u.px.detail)), fd.StatusBad),
			"Not available on this build: "+strings.Join(dead, ", ")+
				". Their patterns no longer match the game's code.")
	}
	if len(unproven) > 0 || !u.px.verified {
		return widgets.WithTip(
			widgets.StatusText(fmt.Sprintf("%d unproven here", len(unproven)), fd.StatusWarn),
			"These resolve on the running build but were confirmed on a different one. "+
				"They should work. Nobody has checked them here.")
	}
	return widgets.StatusText("verified", fd.StatusGood)
}

// patchState is the Patches section's data: the catalog, and what the game says
// is on right now.
type patchState struct {
	catalog  []client.Patch
	sections []string
	on       map[string]bool
	values   map[string]float64
	detail   map[string]client.PatchDetail
	build    string
	verified bool
}

/*
loadCatalog reads the catalog once, through a one-shot CLI run.

Not through the worker and not under sudo: the worker needs a game, and labels
and ranges need no privilege. Reading it at start-up means the section draws its
controls whether or not Terraria is up.
*/
func (u *ui) loadCatalog() {
	u.sh.Load("Reading the patch catalog...", func(ctx context.Context) error {
		out, err := u.runUser(ctx, client.PatchCatalogArgv())
		cat, ok := client.ParsePatchCatalog(out)
		if !ok {
			if err != nil {
				fyne.Do(func() { u.note("patch catalog: " + firstLine(detail(out, err))) })
			}
			return nil
		}
		// Section order is the catalog's order, which groups related patches
		// together; sorting it would scatter them.
		var sections []string
		seen := map[string]bool{}
		for _, p := range cat {
			if !seen[p.Section] {
				seen[p.Section] = true
				sections = append(sections, p.Section)
			}
		}
		fyne.Do(func() {
			u.px.catalog, u.px.sections = cat, sections
			u.sh.Refresh()
		})
		return nil
	})
}

// loadPatches reads which patches are on. This one does need the game, so it is
// quiet when there is none: the status bar already says so.
func (u *ui) loadPatches() {
	u.sh.Load("Reading the patches...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.PatchStatusArgv())
		st, ok := client.ParsePatchStatus(out)
		if !ok {
			if err != nil {
				fyne.Do(func() {
					u.px.on, u.px.values, u.px.detail = nil, nil, nil
					u.px.verified = false
				})
			}
			return nil
		}
		fyne.Do(func() {
			u.px.on, u.px.values, u.px.detail = st.On, st.Values, st.Detail
			u.px.build, u.px.verified = st.Build, st.BuildVerified
			u.sh.Refresh()
		})
		return nil
	})
}

// presetFor names the preset a value corresponds to, or the first one when the
// game is reporting something that is not in the list.
func presetFor(p client.Patch, v float64) string {
	if p.Value == nil || len(p.Value.Presets) == 0 {
		return ""
	}
	for _, pr := range p.Value.Presets {
		if pr.Value == v {
			return pr.Label
		}
	}
	for _, pr := range p.Value.Presets {
		if pr.Value == p.Value.Default {
			return pr.Label
		}
	}
	return p.Value.Presets[0].Label
}

// formatValue renders a patch's current value, or its default when the game has
// not reported one. Integers are shown without a decimal point, because "20
// extra tiles" reads better than "20.000000".
func formatValue(p client.Patch, v float64) string {
	if p.Value == nil {
		return ""
	}
	if v == 0 {
		v = p.Value.Default
	}
	if p.Value.Kind == "i32" {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// numberIn validates a typed value against the patch's own range.
func numberIn(lo, hi float64) fyne.StringValidator {
	return func(s string) error {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return errNotANumber
		}
		if v < lo || v > hi {
			return fmt.Errorf("%g to %g", lo, hi)
		}
		return nil
	}
}
