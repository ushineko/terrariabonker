package gui

import (
	"context"
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
The build gate (spec 036).

Terraria updated from 1.4.5.7 to 1.4.5.8 while the Qt panel was running and the
panel said nothing, because an unrecognised build only ever produced a small
amber banner. The patches are byte patterns derived against one exact build, so
an update has three outcomes -- everything still matches, some of it does, or
none of it -- and the user could not tell which. This asks.

The trigger is the build key rather than the window starting: the case it exists
for is the game being restarted into a new build under a window that is already
open.
*/
type gateState struct {
	// asked is the build keys this window has already gated, so a 2 s status
	// poll asks once rather than every other second.
	asked map[string]bool
	// open is set while the check or the dialog is up.
	open bool
	// unavailable is the cheats this build may not run: the ones recorded as
	// dead when the build was accepted. A cheat here stays off even if a later
	// probe resolves it, because the user chose to run without it.
	unavailable map[string]bool
}

// gateWidth is the dialog's width: wide enough that a cheat's reason wraps to
// two lines rather than twenty.
const gateWidth float32 = 620

// maybeGateBuild asks about a build nobody has decided about yet.
//
// It waits for a player to be in-world. Several cheats hook methods mono
// compiles lazily, so a probe at the main menu reports them as unmatched -- and
// a dialog that says a cheat is dead when it is merely not compiled yet is
// worse than no dialog.
func (u *ui) maybeGateBuild(st *client.Status) {
	if st == nil || st.BuildKey() == "" || st.Name == nil || *st.Name == "" {
		return
	}
	build := st.BuildKey()
	if u.gate.open || u.gate.asked[build] {
		return
	}
	if u.gate.asked == nil {
		u.gate.asked = map[string]bool{}
	}
	u.gate.asked[build] = true
	u.gate.open = true
	u.checkBuild(build)
}

// checkBuild probes the running build, then acts on the report.
func (u *ui) checkBuild(build string) {
	u.sh.Perform("Checking the cheats against this build...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.BuildCheckArgv())
		report, ok := client.ParseBuildCheck(out)
		fyne.Do(func() {
			u.gate.open = false
			if !ok {
				// Unreadable: forget the build so the next status read asks
				// again, rather than treating a failed probe as consent.
				delete(u.gate.asked, build)
				u.note("build check: " + firstLine(detail(out, err)))
				return
			}
			u.applyBuildDecision(report)
		})
		return nil
	})
}

// applyBuildDecision acts on a finished probe: nothing to do, a decision this
// machine already made, or a question.
func (u *ui) applyBuildDecision(r *client.BuildCheck) {
	if r.Recognised {
		if r.Decision == client.DecisionDegraded {
			u.setUnavailable(r.DecidedFailed)
			u.loadPatches()
		}
		return
	}
	u.gate.open = true
	u.askAboutBuild(r)
}

/*
askAboutBuild puts the question on screen.

Two buttons and no third way out: closing the dialog by its window button is not
consent, so the dialog has no dismiss and Exit is spelled out.
*/
func (u *ui) askAboutBuild(r *client.BuildCheck) {
	label, decision := gateChoice(r)
	d := dialog.NewCustomWithoutButtons("Terraria has updated", gateBody(r), u.sh.Window)

	proceed := widget.NewButton(label, func() {
		d.Hide()
		u.gate.open = false
		failed := r.Failed
		if decision == client.DecisionAccepted {
			failed = nil
		}
		u.setUnavailable(failed)
		u.note(fmt.Sprintf("[build] %s recorded as %s", r.Build, decision))
		u.once("recording the build", client.AcceptBuildArgv(decision, failed))
		u.loadPatches()
	})
	proceed.Importance = widget.HighImportance

	leave := widget.NewButton("Exit", func() {
		d.Hide()
		u.gate.open = false
		u.note("[build] " + r.Build + " not accepted -- exiting")
		u.sh.Window.Close()
	})
	leave.Importance = widget.DangerImportance

	d.SetButtons([]fyne.CanvasObject{leave, proceed})
	d.Resize(fyne.NewSize(gateWidth, gateHeight(r)))
	d.Show()
}

// gateHeight grows with the list of dead cheats, which has no fixed length.
const (
	gateBaseHeight float32 = 260
	gateLineHeight float32 = 44
	gateMaxHeight  float32 = 560
)

func gateHeight(r *client.BuildCheck) float32 {
	h := gateBaseHeight + float32(len(r.Failed))*gateLineHeight
	if h > gateMaxHeight {
		return gateMaxHeight
	}
	return h
}

/*
gateChoice is the button the report earns, and what accepting it records.

Everything matching is a different answer from some of it matching, and the
button has to say which one is being agreed to -- "OK" on both would make the
two outcomes look like one.
*/
func gateChoice(r *client.BuildCheck) (label, decision string) {
	if len(r.Failed) == 0 {
		return "Accept this build", client.DecisionAccepted
	}
	return fmt.Sprintf("Continue without %d", len(r.Failed)), client.DecisionDegraded
}

// gateBody is what the dialog says. Built apart from the dialog so it can be
// read in a test without one on screen.
func gateBody(r *client.BuildCheck) fyne.CanvasObject {
	total := len(r.Cheats)
	col := container.NewVBox(
		widgets.Wrapped("This is not a build terrariabonker knows."),
		widgets.DimWrapped("Running: "+r.Build),
	)
	if r.Message != "" {
		col.Add(widgets.DimWrapped(r.Message))
	}

	if len(r.Failed) == 0 {
		col.Add(widgets.Wrapped(fmt.Sprintf(
			"All %d cheats still match their patterns, so the update did not touch "+
				"the code they patch.", total)))
		col.Add(widgets.DimWrapped("Accepting records this build so you are not asked " +
			"again. That means the patterns still match. It does not mean anyone has " +
			"played each cheat on this build."))
		return container.NewVScroll(col)
	}

	col.Add(widgets.StatusText(fmt.Sprintf("%d of %d cheats no longer match on this build:",
		len(r.Failed), total), fd.StatusWarn))
	for _, name := range sortedFailures(r) {
		reason := r.Cheats[name].Reason
		if reason == "" {
			reason = "no match on this build"
		}
		col.Add(widgets.PlainRow(name, reason))
	}
	col.Add(widgets.Wrapped("Continuing switches those off and greys them out. " +
		"Exiting leaves the game alone."))
	return container.NewVScroll(col)
}

// sortedFailures names the dead cheats in a fixed order, so the same report
// reads the same way twice.
func sortedFailures(r *client.BuildCheck) []string {
	out := append([]string(nil), r.Failed...)
	sort.Strings(out)
	return out
}

// setUnavailable records the cheats this build may not run.
func (u *ui) setUnavailable(names []string) {
	u.gate.unavailable = map[string]bool{}
	for _, n := range names {
		u.gate.unavailable[n] = true
	}
}

// gated reports whether a cheat is one the user chose to run without.
func (u *ui) gated(name string) bool { return u.gate.unavailable[name] }
