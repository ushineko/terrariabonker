package gui

import (
	"strings"
	"testing"

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
The output pane is divided by a bar the user can drag, and the position is the
window's rather than the section's.

Sections rebuild on every refresh, and each rebuild makes a new split: without
somewhere outside them to keep the position, a drag would be undone by the next
status poll.
*/
func TestTheOutputDividerSurvivesASectionRebuild(t *testing.T) {
	u := testUI(t)
	u.loadLogOffset()
	require.InDelta(t, defaultLogOffset, u.logOffset, 0.001)

	u.logSplit(widget.NewLabel("a section"))
	require.NotNil(t, u.split)

	u.split.SetOffset(0.42) // the user drags the bar down
	u.logSplit(widget.NewLabel("the same section, rebuilt"))
	require.InDelta(t, 0.42, u.logOffset, 0.001, "the drag is remembered")
	require.InDelta(t, 0.42, u.split.Offset, 0.001, "and applied to the new split")

	u.saveLogOffset()
	u.logOffset = 0
	u.loadLogOffset()
	require.InDelta(t, 0.42, u.logOffset, 0.001, "and kept for the next run")
}

// A stored position outside the bar's range is ignored: obeying it would open
// the window with one of the two panes invisible.
func TestAnImpossibleDividerPositionIsIgnored(t *testing.T) {
	u := testUI(t)
	for _, bad := range []float64{0, 1, -0.5, 4} {
		u.sh.App.Preferences().SetFloat(logOffsetKey, bad)
		u.loadLogOffset()
		require.InDeltaf(t, defaultLogOffset, u.logOffset, 0.001, "%v was obeyed", bad)
	}
}
