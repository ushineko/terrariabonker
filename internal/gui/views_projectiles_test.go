package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/gui/client"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
The editable fields must be the CLI's own.

The window spells them out because it has to draw a control per field, and a
field the CLI does not know is rejected at the far end with a message nobody
sees. So the list is checked against the editor's own table rather than against
the copy written here.
*/
func TestTheProjectileFieldsAreTheOnesTheCLIKnows(t *testing.T) {
	require.Len(t, client.ProjectileFields, len(projectile.Fields),
		"a field was added or dropped")
	for _, f := range client.ProjectileFields {
		got, known := projectile.Fields[f.Name]
		require.Truef(t, known, "%q is not a field the editor knows", f.Name)
		require.Equalf(t, string(got.Kind), f.Kind, "%s is written at a different width", f.Name)
		require.Equalf(t, got.Lo, f.Lo, "%s has a different lower limit", f.Name)
		require.Equalf(t, got.Hi, f.Hi, "%s has a different upper limit", f.Name)
	}
}

/*
One slice's argv is the same argv every time.

The overrides live in maps, and a command line that reorders itself between
calls is miserable to compare in a log or in a test.
*/
func TestOneSliceOfProjectileEditingIsWrittenInAFixedOrder(t *testing.T) {
	argv := client.ProjectileTickArgv(map[int]map[string]float64{
		837: {"scale": 2.5, "tileCollide": 0},
		14:  {"penetrate": -1, "extraUpdates": 3},
	})
	require.Equal(t, []string{
		"projectile-tick", "--json", "--budget", "0.25",
		"--set", "14:extraUpdates=3",
		"--set", "14:penetrate=-1",
		"--set", "837:scale=2.5",
		"--set", "837:tileCollide=0",
	}, argv)

	require.Equal(t, []string{"projectile-tick", "--json", "--budget", "0.25"},
		client.ProjectileTickArgv(nil), "nothing to enforce is still a valid slice")
}

/*
A projectile with no fields left is forgotten entirely.

An entry with an empty field map would keep the editor sweeping the game's
thousand projectile slots for something nobody is editing any more.
*/
func TestUntickingTheLastFieldForgetsTheProjectile(t *testing.T) {
	u := testUI(t)
	u.pj.shoot = 837
	scale := client.ProjectileFields[3]
	pierce := client.ProjectileFields[1]
	require.Equal(t, "scale", scale.Name)
	require.Equal(t, "penetrate", pierce.Name)

	u.storeOverride(scale, true, 2.5)
	u.storeOverride(pierce, true, -1)
	require.Equal(t, map[string]float64{"scale": 2.5, "penetrate": -1}, u.pj.overrides[837])

	u.storeOverride(pierce, false, -1)
	require.Equal(t, map[string]float64{"scale": 2.5}, u.pj.overrides[837])

	u.storeOverride(scale, false, 2.5)
	require.NotContains(t, u.pj.overrides, 837)

	// With no projectile selected there is nothing to store it against.
	u.pj.shoot = 0
	u.storeOverride(scale, true, 2.5)
	require.Empty(t, u.pj.overrides)
}

/*
Two weapons can fire the same projectile, and the section says so.

The overrides are keyed by projectile, so editing one of them edits the other.
Not saying so would make the second weapon look like it changed on its own.
*/
func TestTheSectionSaysWhenTwoWeaponsShareAProjectile(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{757: "Terra Blade", 368: "Excalibur", 9: "Wood"}
	u.pj.known = map[int]int{757: 837, 368: 837, 9: 0}

	require.Equal(t, "Fires projectile 837. Also fired by Excalibur. "+
		"Editing one edits them all.", u.firesLine(757, 837))
	// A projectile nobody else fires is reported without the aside.
	u.pj.known = map[int]int{757: 837}
	require.Equal(t, "Fires projectile 837.", u.firesLine(757, 837))

	require.Contains(t, u.firesLine(9, 0), "fires no projectile")
}

// The loop is not started with nothing to enforce: fifty rounds a second that
// change nothing is work the worker does instead of answering the inventory.
func TestTheEditorRefusesToRunWithNothingTicked(t *testing.T) {
	u := testUI(t)
	u.setProjectiles(true)
	require.False(t, u.projectiles.running())

	u.pj.shoot = 837
	u.storeOverride(client.ProjectileFields[0], true, 0)
	u.setProjectiles(true)
	require.True(t, u.projectiles.running())
	u.setProjectiles(false)
	require.False(t, u.projectiles.running())
}

/*
The section renders, and its controls are off until a weapon is picked.

A box that can be typed in before there is a projectile to write to is a box
whose value goes nowhere, which is worse than one that is plainly disabled.
*/
func TestTheProjectileSectionRendersToAnImage(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{757: "Terra Blade", 3506: "Copper Pickaxe"}
	u.iv.slots[0] = client.ItemSlot{Slot: 0, Type: 757, Stack: 1}
	u.iv.slots[1] = client.ItemSlot{Slot: 1, Type: 3506, Stack: 1}
	u.pj.weapon, u.pj.shoot = 757, 837
	u.pj.says = "Fires projectile 837."
	u.pj.overrides = map[int]map[string]float64{837: {"scale": 3}}

	win := test.NewWindow(u.buildProjectiles())
	defer win.Close()
	win.Resize(fyne.NewSize(1100, 700))

	require.NotPanics(t, func() { _ = win.Canvas().Capture() })
	texts := fynetest.Texts(win.Canvas().Content())
	for _, want := range []string{
		"Projectiles", "Terra Blade", "Fires projectile 837.",
		"Pass through blocks", "Size", "3", "Apply while I play",
	} {
		require.Containsf(t, texts, want, "%q is missing from the section", want)
	}
}
