package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

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

The CLI is cleared after construction, so nothing a test does can spawn sudo or
touch a game. That is deliberate and load-bearing: this is a trainer, and a test
suite that could write to a running process is a test suite that will, eventually,
on someone's machine.

Clearing it afterwards rather than trusting an empty Options is the point. resolve
looks the CLI up on PATH, and on a developer's machine it is installed there -- so
the first version of this helper left every test one call away from running the
real trainer under sudo.
*/
func testUI(t *testing.T) *ui {
	t.Helper()
	fynetest.Sandbox(t)
	app := test.NewApp()
	t.Cleanup(app.Quit)

	u := &ui{version: "test"}
	u.sh = shell.Headless(app, u.shellOptions(Options{}))
	u.cli, u.sudoOK = "", false
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
Every argv this window can build must parse against the real CLI parser.

This is the guardrail the Python side has had all along, reached from Go: the
drift it exists to catch is a builder that emits something the CLI will not
accept, which otherwise shows up as a button that does nothing in a running
game. It caught two on its first run -- `long-reach 20`, written without the
--tiles the parser requires, and a `version --json` that does not exist.

Parsing only. build_parser().parse_args() reaches no Service, opens no process
and writes nothing; a SystemExit out of it is the contract drift.
*/
func TestEveryArgvWeBuildParsesAgainstTheRealCLI(t *testing.T) {
	samples := client.Samples()
	require.NotEmpty(t, samples)

	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			require.Equalf(t, s.Cmd, s.Argv[0], "%s emits %q, declared as %q", s.Name, s.Argv[0], s.Cmd)

			argv, err := json.Marshal(s.Argv)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), parseTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", parseCheck, string(argv)) //nolint:gosec // a fixed script and our own argv
			// From the repository root, because that is where the package is
			// importable from. go test runs in the package directory, and
			// without this every case skipped -- which is how a guardrail
			// silently stops guarding.
			cmd.Dir = repoRoot
			out, err := cmd.CombinedOutput()
			if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
				t.Skip("the Python CLI is not importable here")
			}
			require.NoErrorf(t, err, "%s: %s does not parse: %s", s.Name, s.Argv, out)
			require.Equalf(t, s.Cmd, strings.TrimSpace(string(out)),
				"%s reached a different subcommand", s.Name)
		})
	}
}

// repoRoot is where the Python package is importable from, relative to this
// package's directory. parseTimeout bounds one interpreter start-up.
const (
	repoRoot     = "../.."
	parseTimeout = 30 * time.Second
)

// parseCheck parses an argv with the real CLI parser and prints the subcommand
// it reached, so the test can assert which handler it found rather than only
// that it found one.
const parseCheck = `
import json, sys
from terrariabonker.cli import build_parser
argv = json.loads(sys.argv[1])
args = build_parser().parse_args(argv)
if getattr(args, "func", None) is None:
    sys.exit("no handler")
print(argv[0])
`

/*
Every operation must also be one the privileged worker will serve, or it falls
back to a one-shot CLI run costing ~2.7 s instead of ~2.5 ms -- the difference
between a 1 Hz inventory sync and a window that stutters.

SERVE_OPS is read where it is declared rather than copied, because a copy is a
second spelling to keep in step.
*/
func TestEveryOperationWeSendIsOneTheWorkerWillServe(t *testing.T) {
	ops := serveOps(t)
	require.NotEmpty(t, ops, "could not read SERVE_OPS from the CLI")

	for _, s := range client.Samples() {
		if notServed[s.Argv[0]] {
			continue
		}
		require.Containsf(t, ops, s.Argv[0],
			"%q is not in SERVE_OPS, so the warm worker would refuse it", s.Argv[0])
	}
}

/*
notServed is the operations that must not go through the worker.

freeze is a blocking loop. Sending it to the worker would stop the worker
answering anything else for as long as the cheat is on, which is why the window
runs it as a process of its own. Listed rather than inferred, so that adding an
operation the worker cannot take is a decision someone wrote down.
*/
var notServed = map[string]bool{"freeze": true}

// The one exception has to be real: if freeze ever becomes servable, this list
// is stale and the reason above no longer holds.
func TestTheOnlyUnservedOperationIsTheBlockingOne(t *testing.T) {
	ops := serveOps(t)
	for op := range notServed {
		require.NotContainsf(t, ops, op,
			"%q is servable now, so it should not be run as its own process", op)
	}
}

// serveOps reads the SERVE_OPS frozenset out of the Python CLI, where it is
// declared. Reading it rather than copying it is the point: a copy is a second
// spelling to keep in step.
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

/*
A failed operation is an answer, not a transport problem.

The worker says both with ok:false, and the difference decides whether the
window pays 2.7 s to be told the same thing a second time. Getting this wrong is
invisible -- everything still works, just slowly -- which is why it is pinned.
*/
func TestOnlyARefusalSendsUsBackToTheSlowPath(t *testing.T) {
	require.True(t, declined(errors.New("[ERROR] frobnicate is not served here")))
	require.True(t, declined(errors.New("[ERROR] malformed request: bad")))
	require.True(t, declined(errors.New("the privileged worker is not running")))
	require.True(t, declined(errors.New("the privileged worker exited")))

	require.False(t, declined(errors.New("[ERROR] no running Terraria.exe found")),
		"the game not running is the answer; asking again costs 2.7s to hear it twice")
	require.False(t, declined(errors.New("[ERROR] no player loaded")))
	require.False(t, declined(nil))
}

// The status bar is a row of short facts. The CLI's advice is good and long, so
// the bar gets the verdict and the log gets the advice.
func TestTheStatusBarGetsAVerdictAndTheLogGetsTheAdvice(t *testing.T) {
	out := "[ERROR] no running Terraria.exe found. Is Terraria launched (Windows build under Proton)?"
	require.Equal(t, "not running", shortReason(out, nil))
	require.Equal(t, out, detail(out, nil), "the log keeps every word of it")

	require.Equal(t, "sudo refused", shortReason("sudo: a password is required", nil))
	require.Equal(t, "unreadable", shortReason("", nil))
	require.Equal(t, "boom", detail("", errors.New("boom")))
}
