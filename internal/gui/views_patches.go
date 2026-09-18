package gui

import (
	"context"
	"fmt"
	"strconv"

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
	body := container.NewVBox(widgets.Heading("Patches", "Code written into the running game."))

	switch {
	case len(u.px.catalog) == 0:
		body.Add(widgets.Card("Catalog",
			widgets.Wrapped("The patch catalog has not been read yet."),
			widgets.DimWrapped("It comes from the CLI. If this stays empty, "+cliName+
				" is not on PATH or it failed to run."),
		))
	default:
		for _, section := range u.px.sections {
			body.Add(u.patchCard(section))
		}
	}

	body.Add(u.restoreCard())
	return container.NewBorder(nil, widgets.FixedHeight(u.logWidget(), logHeight), nil, nil,
		container.NewVScroll(body))
}

/*
patchCard is one of the catalog's sections, laid out as a grid.

A form layout rather than a row per patch. It is two columns and it sizes the
first to the widest thing in it, so every value control in the section starts at
the same x. Rows built independently cannot do that: each one is only as wide as
its own contents, and the boxes step in and out down the section according to how
long each label and unit happens to be.

The note spans both columns by taking a row of its own with an empty first cell.
*/
func (u *ui) patchCard(section string) fyne.CanvasObject {
	grid := container.New(layout.NewFormLayout())
	for _, p := range u.px.catalog {
		if p.Section != section {
			continue
		}
		label, value := u.patchRow(p)
		grid.Add(label)
		grid.Add(value)
		grid.Add(widget.NewLabel("")) // the note's empty first cell
		grid.Add(widgets.DimWrapped(p.Note))
	}
	return widgets.Card(section, grid)
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
	return on, value
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
	verdict := widgets.StatusText("verified", fd.StatusGood)
	if !u.px.verified {
		verdict = widgets.StatusText("unverified on this build", fd.StatusWarn)
	}
	if len(u.px.catalog) == 0 {
		verdict = widgets.StatusText("not read", fd.StatusInfo)
	}

	return widgets.Card("Saved patches",
		container.NewHBox(run, widgets.Dim("build"), widget.NewLabel(build), verdict),
		widgets.DimWrapped("A game restart clears every patch. Restore puts back what the "+
			"profile says should be on."),
	)
}

// patchState is the Patches section's data: the catalog, and what the game says
// is on right now.
type patchState struct {
	catalog  []client.Patch
	sections []string
	on       map[string]bool
	values   map[string]float64
	build    string
	verified bool
}

/*
loadCatalog reads the catalog once, through a one-shot CLI run.

Not through the worker: the worker needs a game, and this is labels and ranges.
Reading it at start-up means the section draws its controls whether or not
Terraria is up.
*/
func (u *ui) loadCatalog() {
	u.sh.Load("Reading the patch catalog...", func(ctx context.Context) error {
		out, err := u.runDirect(ctx, client.PatchCatalogArgv())
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
					u.px.on, u.px.values = nil, nil
					u.px.verified = false
				})
			}
			return nil
		}
		fyne.Do(func() {
			u.px.on, u.px.values = st.On, st.Values
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
