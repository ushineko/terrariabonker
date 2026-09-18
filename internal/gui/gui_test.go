package gui

import (
	"os"
	"regexp"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"

	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/fynetest"
	"github.com/ushineko/fynedesygn/shell"
	fdtheme "github.com/ushineko/fynedesygn/theme"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
testUI is a window with no window: the panel's state over a headless shell, with
HOME and the XDG directories pointed at throwaway paths so a test cannot reach
the developer's configuration.

cli is left empty, so nothing a test does can spawn sudo or touch a game. That is
deliberate and load-bearing: this is a trainer, and a test suite that could write
to a running process is a test suite that will, eventually, on someone's machine.
*/
func testUI(t *testing.T) *ui {
	t.Helper()
	fynetest.Sandbox(t)
	app := test.NewApp()
	t.Cleanup(app.Quit)

	u := &ui{version: "test"}
	u.sh = shell.Headless(app, u.shellOptions(Options{}))
	win := test.NewWindow(widget.NewLabel(""))
	u.sh.Window = win
	t.Cleanup(win.Close)
	return u
}

// The flag help lists the section names while parsing flags, before there is a
// Fyne app to construct a theme icon against. Asking for one then makes Fyne log
// "Attempt to access current Fyne app when none is started" once per name.
func TestSectionNamesNeedsNoApp(t *testing.T) {
	names := SectionNames()
	require.Equal(t, []string{
		"Player", "Effects", "Projectiles", "Patches", "Inventory", "Recipes", "Compendium",
	}, names, "the navigation must keep the Qt window's tab order")

	for _, n := range names {
		require.Containsf(t, sectionBuilders(), n, "%q is advertised but has no section", n)
	}
	require.Len(t, sectionBuilders(), len(names), "a section exists that the navigation never shows")
}

// Every section must render in every scheme without a display and without a
// game. A section that panics on an unloaded field is a window that dies on the
// navigation click that reaches it.
func TestEverySectionRendersHeadlesslyInEveryScheme(t *testing.T) {
	for _, scheme := range fdtheme.SchemeNames() {
		t.Run(scheme, func(t *testing.T) {
			u := testUI(t)
			a := u.sh.Appearance()
			a.Scheme = scheme
			u.sh.SetAppearance(a)

			for _, sec := range sections(u) {
				var out fyne.CanvasObject
				require.NotPanicsf(t, func() { out = sec.Build(u.sh) }, "%s did not render", sec.Title())
				require.NotNilf(t, out, "%s rendered nothing", sec.Title())
			}
		})
	}
}

// The status bar has to read before the game does: with nothing attached it says
// so rather than rendering "—/—" as though that were a player.
func TestTheStatusBarSaysWhatItKnowsBeforeAnythingIsAttached(t *testing.T) {
	u := testUI(t)
	segs := u.statusSegments()
	require.NotEmpty(t, segs)
	require.Contains(t, fynetest.Texts(segments(segs)), "not attached")
}

// With a status in hand it reports the player, and colours HP by how much of it
// is left -- this window's whole job is that number.
func TestTheStatusBarReportsThePlayerAndRanksTheirHealth(t *testing.T) {
	u := testUI(t)
	name, hp, maxhp, mana, maxmana := "Corvid", 40, 400, 60, 200
	u.status = &client.Status{
		Name: &name, HP: &hp, MaxHP: &maxhp, Mana: &mana, MaxMana: &maxmana,
		Version: "1.4.5.7", BuildID: "19012345", Build: "1.4.5.7+19012345",
	}
	u.statusOK = true

	texts := fynetest.Texts(segments(u.statusSegments()))
	require.Contains(t, texts, "Corvid")
	require.Contains(t, texts, "40/400")
	require.Contains(t, texts, "60/200")
	require.Contains(t, texts, "1.4.5.7+19012345", "the build key is the identity an AOB is pinned to")

	require.Equal(t, fd.StatusBad, hpStatus(u.status), "a tenth of your health left is not a neutral fact")
	hp = 380
	require.Equal(t, fd.StatusGood, hpStatus(u.status))
}

/*
Every operation this window can send must be one the privileged worker will
serve, or it silently falls back to a one-shot CLI run costing ~2.7 s instead of
~2.5 ms -- which is the difference between a 1 Hz inventory sync and a window
that stutters.

SERVE_OPS is the Python CLI's whitelist and this reads it where it is declared,
so adding a Go operation the worker will not take fails here rather than in the
game. Spec 050 AC5; it grows into the full parity check in phase 6.
*/
func TestEveryOperationWeSendIsOneTheWorkerWillServe(t *testing.T) {
	ops := serveOps(t)
	require.NotEmpty(t, ops, "could not read SERVE_OPS from the CLI")

	for _, argv := range [][]string{
		client.StatusArgv(),
		client.InventoryArgv(),
		client.SetHPArgv("max"),
		client.SetManaArgv("max"),
		client.SetMaxHPArgv(400),
		client.SetMaxManaArgv(200),
		client.FastMiningArgv(),
		client.LongReachArgv(20),
		client.VersionArgv(),
	} {
		require.Containsf(t, ops, argv[0],
			"%q is not in SERVE_OPS, so the warm worker would refuse it", argv[0])
	}
}

// serveOps reads the SERVE_OPS frozenset out of the Python CLI. Reading the
// declaration rather than copying it is the point: a copy is a second spelling
// to keep in step, which is exactly the failure this project has a rule about.
func serveOps(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("../../terrariabonker/cli.py")
	if err != nil {
		t.Skipf("the Python CLI is not here to check against: %v", err)
	}
	block := regexp.MustCompile(`(?s)SERVE_OPS\s*=\s*frozenset\(\{(.*?)\}\)`).FindSubmatch(src)
	if block == nil {
		t.Skip("SERVE_OPS is no longer a frozenset literal; teach this test its new shape")
	}
	var ops []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(string(block[1]), -1) {
		ops = append(ops, m[1])
	}
	return ops
}

// The reply parsers take the last line, because the CLI may print a warning
// before its JSON and a warning must not make a reply unreadable.
func TestAReplyIsReadableThroughWhateverTheCLIPrintedFirst(t *testing.T) {
	st, ok := client.ParseStatus("[WARN] something\n{\"pid\": 42, \"version\": \"1.4.5.7\"}\n")
	require.True(t, ok)
	require.Equal(t, 42, st.PID)

	_, ok = client.ParseStatus("[ERROR] Terraria not found")
	require.False(t, ok, "a failure is not a status")

	_, ok = client.ParseStatus("")
	require.False(t, ok)
}

// splitLines drops the blank tail a subprocess leaves, so the log does not grow
// an empty row per command.
func TestLogLinesDropTheTrailingBlank(t *testing.T) {
	require.Equal(t, []string{"wrote 400", "ok"}, splitLines("wrote 400\nok\n"))
	require.Empty(t, splitLines("\n\n"))
}

// segments wraps the status bar's objects so the text walker can read them.
func segments(objs []fyne.CanvasObject) fyne.CanvasObject {
	return container.NewHBox(objs...)
}
