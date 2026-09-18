package gui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
Starting the game is a header action, as it was a header button in the Qt panel.

It belongs beside Refresh rather than inside a section: the game is what every
section is talking to, and a window with no game found is exactly when somebody
wants it.
*/
func TestTheHeaderCanStartTheGame(t *testing.T) {
	u := testUI(t)
	actions := u.headerActions()
	require.Len(t, actions, 2)
	labels := strings.Join(fynetest.Texts(actions[0]), " ") + " " +
		strings.Join(fynetest.Texts(actions[1]), " ")
	require.Contains(t, labels, "Launch Terraria")
	require.Contains(t, labels, "About")
}

// Terraria is started through Steam, because Steam is what sets Proton up
// around it. Running the game's own binary would start it without any of that.
func TestTheGameIsStartedThroughSteam(t *testing.T) {
	require.Equal(t, "steam://rungameid/105600", steamURL)
}

// A command that is not installed is an error rather than a silence, which is
// what lets the launch fall back to the desktop's own opener.
func TestStartingSomethingThatIsNotThereFails(t *testing.T) {
	require.Error(t, startDetached("terrariabonker-no-such-command"))
}

// About names the game it is looking at, and says so plainly when there is none
// rather than showing an empty row.
func TestAboutNamesTheRunningGame(t *testing.T) {
	u := testUI(t)
	require.Equal(t, "no game found", u.aboutBuild())

	u.status, u.statusOK = &client.Status{Version: "1.4.5.8", BuildID: "249",
		Build: "1.4.5.8+249"}, true
	require.Equal(t, "Terraria 1.4.5.8+249", u.aboutBuild())

	u.status = &client.Status{}
	require.Equal(t, "Terraria an unreadable build", u.aboutBuild())
}

/*
The output pane is divided by a bar the user can drag.

The position is the shell's, not this window's: every section rebuilds on the
two-second status poll and each rebuild makes a new split, so a drag kept here
would be undone before anyone let go of the mouse.
*/
func TestTheOutputDividerIsTheShellsToRemember(t *testing.T) {
	u := testUI(t)
	body := u.logSplit(widget.NewLabel("a section"))
	split, ok := body.(*container.Split)
	require.True(t, ok)
	require.InDelta(t, defaultLogOffset, split.Offset, 0.001)

	split.SetOffset(0.42) // the user drags the bar down
	again, ok := u.logSplit(widget.NewLabel("the same section, rebuilt")).(*container.Split)
	require.True(t, ok)
	require.InDelta(t, 0.42, again.Offset, 0.001, "the drag survives the rebuild")
}

// Every section's output is one pane as far as anyone using it is concerned, so
// they share a key and the bar does not jump when the section changes.
func TestEverySectionsOutputIsTheSamePane(t *testing.T) {
	require.Equal(t, "output", logSplitKey)
}
