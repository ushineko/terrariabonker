package gui

import (
	"context"
	"fmt"
	"os/exec"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/dialogs"
	"github.com/ushineko/fynedesygn/widgets"
)

// steamAppID is Terraria on Steam. The game is started through Steam rather
// than by running its binary, because Steam is what sets up Proton around it.
const steamAppID = "105600"

// steamURL is what both openers are handed.
const steamURL = "steam://rungameid/" + steamAppID

// headerActions are the buttons beside the shell's own Refresh.
func (u *ui) headerActions() []fyne.CanvasObject {
	launch := widget.NewButton("Launch Terraria", func() { u.launchGame() })
	about := widget.NewButton("About", func() { u.showAbout() })
	return []fyne.CanvasObject{
		widgets.WithTip(launch, "Starts the game through Steam."),
		about,
	}
}

/*
launchGame starts Terraria through Steam.

Unprivileged and detached, and that is not tidiness: Steam refuses to run as
root, so this is the one action in the window that must not go anywhere near the
sudo wrapper everything else uses.
*/
func (u *ui) launchGame() {
	if err := startDetached("steam", steamURL); err == nil {
		u.note("[launch] " + steamURL)
		u.sh.Flash("Starting Terraria.", fd.StatusInfo)
		return
	}
	// No steam on PATH: hand the URL to the desktop, which knows what a
	// steam:// link is if Steam is installed as a Flatpak or a Snap.
	if err := dialogs.OpenPath(steamURL); err != nil {
		u.note("[launch] could not start Terraria: " + err.Error())
		u.sh.Flash("Could not start Terraria. Is Steam installed?", fd.StatusBad)
		return
	}
	u.note("[launch] " + steamURL + " (through the desktop)")
	u.sh.Flash("Starting Terraria.", fd.StatusInfo)
}

/*
startDetached runs a command and lets go of it.

The game outlives this window, so its process is not tied to a context of ours
and its exit is not this program's business. Waiting in the background is only
to reap it.
*/
func startDetached(name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // a fixed command and a fixed URL
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// aboutSize is the About dialog, sized to hold its paragraphs without scrolling.
const (
	aboutWidth  float32 = 560
	aboutHeight float32 = 420
)

// showAbout says what this is and who is owed credit for parts of it.
func (u *ui) showAbout() {
	dialogs.ShowDetail(u.sh.Window, "About "+cliName,
		container.NewVScroll(container.NewVBox(
			widgets.Heading(cliName+" "+widgets.OrNone(u.version, "dev"),
				"A live-memory trainer and item editor for Terraria under Proton."),
			widgets.Wrapped("It finds the player in the running game and edits what it "+
				"finds. No addresses are hardcoded, and nothing is written to the save."),
			widgets.DimWrapped("Cheat sites are derived with Cheat Engine's mono "+
				"dissector. Nothing needs Cheat Engine at runtime."),
			widgets.DimWrapped("Several code patches -- pickup range, spawn rate, the "+
				"drop-chance floor and map-ping teleport -- are ported from the FearLess "+
				"Forums \"TerrariaReGrind\" Cheat Engine table. The reverse-engineering "+
				"credit for those hooks belongs to its authors. The sites used here were "+
				"re-derived against this game build."),
			widgets.PlainRow("Running", u.aboutBuild()),
		)), aboutWidth, aboutHeight)
}

// aboutBuild is the game this window is looking at, or what is missing.
func (u *ui) aboutBuild() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.statusOK || u.status == nil {
		return "no game found"
	}
	return "Terraria " + widgets.OrNone(u.status.BuildKey(), "an unreadable build")
}
