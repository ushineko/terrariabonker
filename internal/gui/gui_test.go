package gui

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
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

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/gui/client"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/service"
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

	root := cli.Root()
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			require.Equalf(t, s.Cmd, s.Argv[0], "%s emits %q, declared as %q",
				s.Name, s.Argv[0], s.Cmd)

			cmd, rest, err := root.Find(s.Argv)
			require.NoErrorf(t, err, "%s: %v reaches no command", s.Name, s.Argv)
			require.Equalf(t, s.Cmd, cmd.Name(), "%s reached a different subcommand", s.Name)
			require.NoErrorf(t, cmd.ParseFlags(rest),
				"%s: %v has a flag the CLI does not take", s.Name, s.Argv)
			require.NoErrorf(t, cmd.ValidateArgs(cmd.Flags().Args()),
				"%s: %v has arguments the CLI does not take", s.Name, s.Argv)
		})
	}
}

/*
Every operation the window sends to the worker must be one the worker will
serve, or it falls back to a one-shot CLI run costing ~2.7 s instead of ~2.5 ms
-- the difference between a 1 Hz inventory sync and a window that stutters.

The allowlist is read where it is declared rather than copied, because a copy is
a second spelling to keep in step.
*/
func TestEveryOperationWeSendToTheWorkerIsOneItWillServe(t *testing.T) {
	require.NotEmpty(t, cli.ServeOps, "the worker serves nothing")

	for _, s := range client.Samples() {
		if _, direct := sentDirectly[s.Name]; direct {
			continue
		}
		require.Truef(t, cli.ServeOps[s.Argv[0]],
			"%q is not served, so the warm worker would refuse it", s.Argv[0])
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
	"ExtractSpritesArgv": "writes the icon cache under the user's home; run through " +
		"sudo it would write it as root and the unprivileged extractor could never " +
		"rewrite it",
	"ExtractSpritesArgv/force": "as above",
	"ExtractRecipesArgv":       "reads the game's files to disk, unprivileged, as above",
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
Every field of a status reply decodes into the struct that reads it.

The argv direction has been checked since the port began: every command the
window can send is parsed by the real CLI. The reply direction never was, and it
broke in the way that costs most -- compat_level is a word ("exact", "hotfix")
and was declared an int, so encoding/json rejected the whole document. With no
game the CLI errors and the bar shows the error, so it looked fine; with a game
the status arrived, failed to decode, and the window reported the build as
unreadable, ran no build gate and never auto-restored.

So the reply is built by the thing that emits it and read by the thing that
reads it, and the two are different packages that share only this shape.
*/
func TestEveryStatusFieldDecodesIntoTheStructThatReadsIt(t *testing.T) {
	emitted := cliStatusReply(t)

	var loose map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(emitted), &loose))
	require.NotEmpty(t, loose, "the CLI emitted no fields")

	got := reflect.TypeOf(client.Status{})
	checked := 0
	for i := range got.NumField() {
		tag := strings.Split(got.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		_, carried := loose[tag]
		require.Truef(t, carried, "the window reads %q and the CLI does not emit it", tag)
		checked++
	}
	require.GreaterOrEqual(t, checked, 8, "the reply's fields are not being checked at all")

	// And the reply the CLI really emitted decodes, whole, into that struct.
	st, ok := client.ParseStatus(emitted)
	require.True(t, ok, "the window could not decode the CLI's own status")
	require.NotEmpty(t, st.CompatLevel, "the compatibility level is a word, and it is missing")
}

/*
cliStatusReply is one `status --json` reply, from the command tree itself.

Run against no game at all: the fields are all there whether or not a player is
loaded, which is the state the window spends most of its time in.
*/
func cliStatusReply(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr strings.Builder
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Attach: func() (*cli.Game, error) {
			return &cli.Game{Svc: service.New(newEmptyMem(), -1), PID: os.Getpid()}, nil
		},
	})
	require.Equalf(t, cli.ExitOK,
		app.Execute(t.Context(), []string{"status", "--json"}, &stdout, &stderr),
		"the CLI could not report a status: %s", stderr.String())
	return stdout.String()
}

// emptyMem is a game with nothing in it, which is what the window sees before a
// world is loaded.
type emptyMem struct{ *memtest.FakeMem }

func (emptyMem) ExePath() string { return "" }

func newEmptyMem() emptyMem { return emptyMem{memtest.New(0x10000000, 0x1000)} }

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
