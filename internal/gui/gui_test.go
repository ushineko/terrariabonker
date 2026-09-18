package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
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
Every operation the window sends to the worker must be one the worker will
serve, or it falls back to a one-shot CLI run costing ~2.7 s instead of ~2.5 ms
-- the difference between a 1 Hz inventory sync and a window that stutters.

SERVE_OPS is read where it is declared rather than copied, because a copy is a
second spelling to keep in step.
*/
func TestEveryOperationWeSendToTheWorkerIsOneItWillServe(t *testing.T) {
	ops := serveOps(t)
	require.NotEmpty(t, ops, "could not read SERVE_OPS from the CLI")

	for _, s := range client.Samples() {
		if _, direct := sentDirectly[s.Name]; direct {
			continue
		}
		require.Containsf(t, ops, s.Argv[0],
			"%q is not in SERVE_OPS, so the warm worker would refuse it", s.Argv[0])
	}
}

/*
sentDirectly is the operations the window deliberately does not send to the
worker, and why.

Keyed by builder rather than by subcommand, because `patch catalog` is sent
directly while `patch status` is not -- listing "patch" would excuse both. Every
entry is a decision someone wrote down rather than something inferred.
*/
var sentDirectly = map[string]string{
	"FreezeArgv": "a blocking loop; through the worker it would stop the worker " +
		"answering anything else for as long as the cheat is on",
	"FreezeArgv/none": "as above",
	"PatchCatalogArgv": "static data; the worker connects to a Service before it " +
		"dispatches, so through it this would need a running game -- and the controls " +
		"it describes are drawn before one is attached",
	"PrefixesArgv": "static data, for the same reason as the patch catalog",
	"ExtractSpritesArgv": "writes the icon cache under the user's home; run through " +
		"sudo it would write it as root and the unprivileged extractor could never " +
		"rewrite it",
	"ExtractSpritesArgv/force": "as above",
	"ExtractRecipesArgv":       "reads the game's files to disk, unprivileged, as above",
	"RecipesArgv":              "static data, read from the cache that extract-recipes wrote",
	"NamesArgv": "bundled data, and the reason it is bundled: through the worker it " +
		"would need a running game, and a recipe book that works offline has to be " +
		"able to say \"Terra Blade\" rather than 757",
}

// Each exception must name a builder that exists, or the list is stale and
// quietly excusing nothing.
func TestEveryDirectExceptionNamesARealBuilder(t *testing.T) {
	have := map[string]bool{}
	for _, s := range client.Samples() {
		have[s.Name] = true
	}
	for name, why := range sentDirectly {
		require.Truef(t, have[name], "%q is not a builder any more", name)
		require.NotEmptyf(t, why, "%q is excused without a reason", name)
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

/*
Every field of a status reply must decode into the struct that reads it.

The argv direction has been checked since the port began: every command the
window can send is parsed by the real CLI. The reply direction never was, and it
broke in the way that costs most -- compat_level is a word ("exact", "hotfix")
and was declared an int, so encoding/json rejected the whole document. With no
game the CLI errors and the bar shows the error, so it looked fine; with a game
the status arrived, failed to decode, and the window reported the build as
unreadable, ran no build gate and never auto-restored.

So this asks the Python for the types its own snapshot carries, the way the argv
test asks it to parse an argv, rather than trusting what is written here.
*/
func TestEveryStatusFieldDecodesIntoTheStructThatReadsIt(t *testing.T) {
	want := pythonSnapshotTypes(t)

	got := reflect.TypeOf(client.Status{})
	checked := 0
	for i := range got.NumField() {
		f := got.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		pyType, known := want[tag]
		if !known {
			continue // built in cmd_status rather than carried by the snapshot
		}
		checked++
		require.Truef(t, fits(pyType, f.Type), "%s is %s in Python and %s here",
			tag, pyType, f.Type)
	}
	require.GreaterOrEqual(t, checked, 8, "the reply's fields are not being checked at all")
}

// fits reports whether a Go type can hold what the Python annotation describes.
// An optional Python field may be a pointer here -- "absent" and "zero" are
// different facts for HP -- or a plain value when zero is answer enough.
func fits(pyType string, goType reflect.Type) bool {
	if goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	switch strings.TrimSuffix(strings.ReplaceAll(pyType, " ", ""), "|None") {
	case "str":
		return goType.Kind() == reflect.String
	case "int":
		return goType.Kind() == reflect.Int
	case "float":
		return goType.Kind() == reflect.Float64
	case "bool":
		return goType.Kind() == reflect.Bool
	}
	return true // a shape this test does not model; the decode test below covers it
}

// pythonSnapshotTypes is what the CLI's own status snapshot carries, by field.
func pythonSnapshotTypes(t *testing.T) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), parseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", snapshotTypes) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)

	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got))
	require.NotEmpty(t, got)
	return got
}

// snapshotTypes dumps the annotations of the two records cmd_status reads.
const snapshotTypes = `
import json
from terrariabonker.service import PlayerState, Snapshot
out = {}
for cls in (Snapshot, PlayerState):
    out.update({k: str(v) for k, v in cls.__annotations__.items()})
print(json.dumps(out))
`

/*
A status reply of the shape the CLI emits decodes whole.

The type check above is the contract; this is the document, so a field renamed
on either side shows up as well as one retyped.
*/
func TestAStatusReplyDecodesWhole(t *testing.T) {
	const reply = `{"pid": 4242, "version": "1.4.5.8", "compat_level": "exact", ` +
		`"buildid": "24893155", "build": "1.4.5.8+24893155", "copies": 2, ` +
		`"name": "Nakama", "hp": 380, "max_hp": 400, "mana": 200, "max_mana": 200, ` +
		`"world": ["Terraria", 12345]}`

	st, ok := client.ParseStatus(reply)
	require.True(t, ok, "the shape the CLI emits must decode")
	require.Equal(t, 4242, st.PID)
	require.Equal(t, "exact", st.CompatLevel)
	require.Equal(t, "1.4.5.8+24893155", st.BuildKey())
	require.NotNil(t, st.HP)
	require.Equal(t, 380, *st.HP)
	require.Equal(t, "Nakama", *st.Name)

	// And the same reply with no player in it, which is the ordinary state
	// before a world is loaded.
	empty, ok := client.ParseStatus(`{"pid": 4242, "version": "1.4.5.8", ` +
		`"compat_level": "exact", "buildid": "", "build": "1.4.5.8", "copies": 1, ` +
		`"name": null, "hp": null, "max_hp": null, "mana": null, "max_mana": null, ` +
		`"world": null}`)
	require.True(t, ok)
	require.Nil(t, empty.Name)
	require.Nil(t, empty.HP)
}
