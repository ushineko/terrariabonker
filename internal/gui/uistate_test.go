package gui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/shell"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
The window comes back the way it was left.

The Qt panel kept this in a file of its own under ~/.cache. It is one section of
the shell's settings file now, which is the same idea with one file instead of
two.
*/
func TestTheWindowRemembersHowItWasLeft(t *testing.T) {
	u := testUI(t)
	u.startWatches()

	u.fx.bait, u.fx.power, u.fx.stack = 42, 7, 15
	u.fx.recast, u.fx.buffPower = true, true
	u.pj.weapon = 757
	u.pj.overrides = map[int]map[string]float64{837: {"scale": 2.5}}
	u.potions.set(true)
	u.saveState()

	next := testUI(t)
	next.sh = u.sh // the same settings file
	next.startWatches()
	next.loadState()

	require.Equal(t, 42, next.fx.bait)
	require.Equal(t, 7, next.fx.power)
	require.Equal(t, 15, next.fx.stack)
	require.True(t, next.fx.recast)
	require.True(t, next.fx.buffPower)
	require.Equal(t, 757, next.pj.weapon)
	require.Equal(t, map[int]map[string]float64{837: {"scale": 2.5}}, next.pj.overrides)
	require.True(t, next.potions.running(), "a cheat that was on comes back on")
	require.False(t, next.catch.running(), "and one that was not, does not")

	next.shutdown()
	u.shutdown()
}

/*
A number is restored before the switch that reads it.

A watch reads the number beside it on its first round, so arming one before its
value is back would send one round at the default -- bait topped to 30 when the
user had asked for 42.
*/
func TestNumbersComeBackBeforeTheSwitchesThatReadThem(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	u.fx.bait = 42
	u.fishing.set(true)
	u.saveState()
	u.shutdown()

	next := testUI(t)
	next.sh = u.sh
	next.startWatches()
	next.loadState()
	require.True(t, next.fishing.running())
	require.Equal(t, 42, next.fx.bait, "the round it is about to send uses the saved bait")
	next.shutdown()
}

/*
The projectile editor's overrides come back; the editor itself does not.

It writes to whatever is in flight fifty times a second. Starting that from a
window that has just opened onto a game nobody has looked at yet is more than a
restored setting should do unasked.
*/
func TestTheProjectileEditorComesBackDisarmed(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	u.pj.shoot = 837
	u.storeOverride(client.ProjectileFields[3], true, 2.5)
	u.projectiles.set(true)
	u.saveState()
	u.shutdown()

	next := testUI(t)
	next.sh = u.sh
	next.startWatches()
	next.loadState()
	require.NotEmpty(t, next.pj.overrides, "what it was set to is kept")
	require.False(t, next.projectiles.running(), "but it does not start itself")
	next.shutdown()
}

// A window with nothing saved keeps its defaults rather than zeroing them.
func TestAWindowWithNothingSavedKeepsItsDefaults(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	u.fx.bait, u.fx.power, u.fx.stack = defaultBait, defaultPower, defaultStack
	u.loadState()
	require.Equal(t, defaultBait, u.fx.bait)
	require.Equal(t, defaultPower, u.fx.power)
	require.Equal(t, defaultStack, u.fx.stack)
	u.shutdown()
}

// Every shape is offered: the grid and the two tables would each rather have
// the width than the list of seven words beside them.
func TestTheWindowOffersEveryNavigationShape(t *testing.T) {
	require.Equal(t, []shell.NavMode{shell.NavLabels, shell.NavIcons, shell.NavHidden}, navModes)
	require.Equal(t, []shell.NavPlacement{shell.NavLeft, shell.NavTop}, navPlacements)
}
