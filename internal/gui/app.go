/*
Package gui is terrariabonker's desktop control panel.

It runs unprivileged and reaches the game only by shelling to the CLI under sudo,
which is the architecture rule this project has always had (AGENTS.md): memory
access needs root, and a window must not. That rule is about privilege, not about
which language the far side is written in, so it survives the port to Go and will
survive the rest of it (spec 051). Nothing in this package reads another
process's memory, opens /proc, or names a game offset.

Two transports, both the CLI's: one long-lived `serve` worker taking JSON lines,
because locating the player is ~99% of a read and a warm worker turns 2.7 s into
2.5 ms; and a one-shot run per action when the worker will not start or will not
serve that operation. internal/gui/client is the only place that knows either
contract.

The window itself -- navigation, status bar, progress popup, banners, dialogs --
is fynedesygn's shell. See spec 050.
*/
package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/dialogs"
	"github.com/ushineko/fynedesygn/logpane"
	"github.com/ushineko/fynedesygn/shell"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// appID names the preference store and, on Wayland, the window's app_id, which
// compositors match to the desktop entry of the same basename.
const appID = "io.ushineko.terrariabonker"

// cliName is the CLI this window drives. install.sh puts it on PATH as a symlink
// in ~/.local/bin, and that is the one entry point both front ends share.
const cliName = "terrariabonker"

// ui holds the window's state.
type ui struct {
	// sh is the window. The shell stores itself here through Options.OnCreate
	// before it builds the first section, so every builder can rely on it.
	sh      *shell.Shell
	version string

	// cli is the resolved path to the CLI, and sudoOK records whether sudo runs
	// without a password. Without passwordless sudo every memory action fails,
	// and it must fail loudly: there is no TTY to prompt on, so a window that
	// waited for a password would just sit there doing nothing.
	cli    string
	sudoOK bool

	// work is the privileged worker, or nil. Operations try it first and fall
	// back to a one-shot CLI run.
	work *helper

	// log is the window's output, the counterpart of the Qt panel's log box.
	log *logpane.Pane

	// The Effects loops. Built once with the window, not with the section, so
	// a cheat survives navigating away from the controls that started it.
	freeze  *freezer
	potions *watch
	fishing *watch
	buffs   *watch
	catch   *watch
	sell    *watch
	// fx is what those loops read and what their controls write. Held on the
	// window for the same reason: a section rebuild must not reset a running
	// cheat's settings.
	fx effectState
	// px is the Patches section's catalog and the game's current state.
	px patchState
	// iv is the Inventory grid: the slots, what each cell was last drawn from,
	// and the catalogs the cells read names out of.
	iv inventoryState
	// inventory is the 1 Hz sync. A watch like the others, because that is what
	// it is: a loop the window holds up while it is open.
	inventory *watch
	// statusPoll keeps the status bar current and is the build gate's trigger.
	statusPoll *watch
	// sprites is the item icon cache on disk.
	sprites *sprites
	// cp is the Compendium's catalog and what it is showing of it.
	cp compendiumState
	// rc is the recipe book and the two indexes into it.
	rc recipeState
	// gate is the build check: which builds have been asked about, and which
	// cheats this one may not run.
	gate gateState
	// pj is the projectile editor, and projectiles is the loop that holds its
	// edits on whatever is in flight.
	pj          projState
	projectiles *watch
	// rs is auto-restore: which game the profile was last put back into, and
	// how far the current attempt has got.
	rs restoreState

	mu        sync.Mutex
	status    *client.Status
	statusOK  bool
	statusMsg string
}

// effectState is the Effects section's settings, and the little each loop
// remembers between rounds.
type effectState struct {
	god, mana                       bool
	buffPower, buffSonar, buffCrate bool
	recast                          bool
	bait, power, stack              int
	// kitDone is set once the kit round has run. After that the service would
	// find gear and do nothing, so the round trip is not worth making again.
	kitDone   bool
	whitelist []int
	sellPick  int
	sellList  *widget.List
}

// sectionTitles is the navigation in order, which is the order the Qt window's
// tabs were in.
//
// The names live here, apart from the icons, because a theme icon cannot be
// constructed before an app exists.
var sectionTitles = []string{
	"Player", "Effects", "Projectiles", "Patches", "Inventory", "Recipes", "Compendium",
}

// sectionEntry is what a section is made of: a deferred icon and its builder.
type sectionEntry struct {
	icon  func() fyne.Resource
	build func(*ui) fyne.CanvasObject
}

// sectionBuilders is what each section is made of. sections walks sectionTitles
// and looks each one up here, so a title with no builder is a missing section
// rather than a silently different list.
func sectionBuilders() map[string]sectionEntry {
	return map[string]sectionEntry{
		"Player":      {theme.AccountIcon, (*ui).buildPlayer},
		"Effects":     {theme.MediaPlayIcon, (*ui).buildEffects},
		"Patches":     {theme.SettingsIcon, (*ui).buildPatches},
		"Inventory":   {theme.StorageIcon, (*ui).buildInventory},
		"Projectiles": {theme.MailSendIcon, (*ui).buildProjectiles},
		"Recipes":     {theme.ListIcon, (*ui).buildRecipes},
		"Compendium":  {theme.HelpIcon, (*ui).buildCompendium},
	}
}

// sections is the navigation as the shell takes it. A nil u is enough for the
// titles, which is all SectionNames needs.
func sections(u *ui) []shell.Section {
	builders := sectionBuilders()
	out := make([]shell.Section, 0, len(sectionTitles))
	for _, title := range sectionTitles {
		b, ok := builders[title]
		if !ok {
			continue
		}
		out = append(out, shell.NewSection(title, b.icon,
			func(*shell.Shell) fyne.CanvasObject { return b.build(u) }))
	}
	return out
}

// SectionNames lists the navigation entries, for --section and for a capture
// script to iterate. Reads the titles rather than building the sections: this is
// called while parsing flags, before there is an app to hang an icon on.
func SectionNames() []string { return shell.Names(sections(nil)) }

// Options configure a run. Section and Scheme exist so a capture script can
// deep-link into the window without clicking through it.
type Options struct {
	Version string
	Section string // navigation entry to open on; empty means the first
	Scheme  string // colour scheme to force for this run; empty means the saved one
	CLI     string // path to the terrariabonker CLI; empty resolves it from PATH
}

// Run opens the window and blocks until it is closed.
func Run(o Options) {
	release, holder, ok := takeLock(lockPath())
	if !ok {
		refuse(alreadyRunning(holder))
		return
	}
	defer release()

	u := &ui{version: o.Version}
	shell.Run(u.shellOptions(o))
}

/*
refuse says why the window is not opening, and then closes.

Its own small window rather than a line on stderr: this is usually launched from
a desktop icon, and a message printed to a terminal nobody is looking at is the
same as no message. Printed as well, for the times it is started from a shell.
*/
func refuse(why string) {
	fmt.Fprintln(os.Stderr, why)
	a := app.NewWithID(appID)
	a.SetIcon(appIcon())
	w := a.NewWindow(cliName)
	w.SetContent(container.NewPadded(container.NewVBox(
		widgets.Heading(cliName+" is already running", ""),
		widgets.Wrapped(why),
		widget.NewButton("Close", w.Close),
	)))
	w.Resize(fyne.NewSize(refuseWidth, refuseHeight))
	w.CenterOnScreen()
	w.ShowAndRun()
}

// The refusal window is sized to hold its two paragraphs without scrolling.
const (
	refuseWidth  float32 = 520
	refuseHeight float32 = 260
)

// shellOptions describes this program to the shell.
func (u *ui) shellOptions(o Options) shell.Options {
	return shell.Options{
		AppID:         appID,
		Name:          cliName,
		Version:       o.Version,
		Icon:          appIcon(),
		Sections:      sections(u),
		Section:       o.Section,
		Scheme:        o.Scheme,
		Header:        func(*shell.Shell) []fyne.CanvasObject { return u.headerActions() },
		NavModes:      navModes,
		NavPlacements: navPlacements,
		StatusBar:     func(*shell.Shell) []fyne.CanvasObject { return u.statusSegments() },
		OnCreate:      func(s *shell.Shell) { u.onCreate(s, o) },
		OnStart:       func(*shell.Shell) { u.start() },
		OnStop:        func(*shell.Shell) { u.shutdown() },
		OnInvalidate:  func(*shell.Shell) { u.statusOK = false },
	}
}

/*
onCreate is everything a section may rely on, before the first one is built.

The shell builds a section during construction, so anything a builder touches
has to exist by now. The Effects controls read the loops to decide whether their
switches are on, and building them in OnStart instead meant a window opened with
--section Effects dereferenced a nil watch and died -- caught by the test that
renders every section headlessly, which is the cheapest place to find it.
*/
func (u *ui) onCreate(s *shell.Shell, o Options) {
	u.sh = s
	u.resolve(o)
	u.fx.bait, u.fx.power, u.fx.stack = defaultBait, defaultPower, defaultStack
	u.iv.slots = map[int]client.ItemSlot{}
	u.iv.shown = map[int]client.ItemSlot{}
	u.iv.cells = map[int]*fyne.Container{}
	u.sprites = newSprites()
	u.cp.kind, u.cp.picked = anyKind, -1
	u.gate.asked = map[string]bool{}
	u.gate.unavailable = map[string]bool{}
	u.startWatches()
	// After the watches exist, because restoring a switch arms one.
	u.loadState()
}

// resolve finds the CLI and works out whether sudo will run without a prompt.
// Before the first section is built, because the status bar reports both from
// every section.
func (u *ui) resolve(o Options) {
	u.cli = o.CLI
	if u.cli == "" {
		if p, err := exec.LookPath(cliName); err == nil {
			u.cli = p
		}
	}
	u.sudoOK = passwordlessSudo()
}

// start brings up the privileged worker and the first status read.
func (u *ui) start() {
	if u.cli == "" {
		u.statusMsg = "not installed"
		u.sh.Flash(cliName+" is not on PATH. Run install.sh, or pass --cli.", fd.StatusBad)
		return
	}
	if !u.sudoOK {
		// Said once, plainly, rather than failing the same way on every button.
		u.sh.Flash("sudo needs a password. Memory operations will fail until "+
			"passwordless sudo is set up for "+cliName+".", fd.StatusWarn)
	}
	u.startWorker()
	u.loadCatalog()
	u.loadPrefixes()
	u.loadStatus()
	u.pollStatus()
	u.loadPatches()
	u.loadSellList()
	u.loadItemNames()
	u.loadNames()
	u.loadRecipes()
	u.syncInventory()
}

// startWorker brings up one `serve` process. Failing is not fatal: every
// operation falls back to a one-shot CLI run, which is what the window did
// before the worker existed. It is a performance feature, not a dependency.
func (u *ui) startWorker() {
	if !u.sudoOK {
		return
	}
	h, err := newHelper("sudo", append(sudoPrefix(u.cli), "serve"), u.note)
	if err != nil {
		u.note("worker did not start: " + err.Error())
		return
	}
	u.work = h
}

// sudoPrefix is the argv between `sudo` and the subcommand.
//
// -n is non-interactive: without passwordless sudo the call fails at once with a
// clear message, where without it the process would block on a password prompt
// that has no terminal to appear on. -E keeps the environment, which is how the
// CLI finds the game the same way the user's shell would.
func sudoPrefix(cli string) []string {
	return []string{"-n", "-E", cli}
}

// passwordlessSudo reports whether sudo runs without a password, which is what
// every memory operation here needs. Bounded, because this runs before the first
// section is drawn and a sudo that hangs would hang the window's startup.
func passwordlessSudo() bool {
	ctx, cancel := context.WithTimeout(context.Background(), sudoProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

// sudoProbeTimeout bounds the passwordless-sudo check.
const sudoProbeTimeout = 5 * time.Second

// run performs one CLI operation, through the warm worker when it will take it
// and as a one-shot sudo run otherwise. Call off the UI thread.
//
// The context is the operation's: cancelling it kills the subprocess rather than
// leaving a sudo run going after the window has stopped waiting for it.
func (u *ui) run(ctx context.Context, argv []string) (string, error) {
	if u.cli == "" {
		return "", fmt.Errorf("%s is not on PATH", cliName)
	}
	if u.work.Available() {
		out, err := u.work.Do(argv)
		switch {
		case err == nil:
			return out, nil
		case !declined(err):
			// The operation ran and failed -- the game is not up, the player is
			// not loaded, the write was refused. That is the answer, not a
			// transport problem, and running it again costs 2.7 s to be told
			// the same thing.
			return out, err
		}
		// Refused or dead: the serve whitelist does not cover everything, and a
		// worker that has gone must not take the window with it.
		u.note("running " + argv[0] + " directly")
	}
	cmd := exec.CommandContext(ctx, "sudo", append(sudoPrefix(u.cli), argv...)...) //nolint:gosec // argv comes from the client package
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

/*
declined reports whether the worker turned the command away rather than running
it and failing.

The protocol says both with ok:false, so the text is the only thing that
distinguishes them -- cmd_serve answers "<op> is not served here" for an op
outside SERVE_OPS, and a malformed request likewise. Everything else with
ok:false is the operation's own verdict, which is an answer and not a reason to
run it a second time.
*/
func declined(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "is not served here") ||
		strings.Contains(msg, "malformed request") ||
		strings.Contains(msg, "worker is not running") ||
		strings.Contains(msg, "worker exited")
}

/*
shutdown stops everything the window is holding up.

The cheats on the Effects section are held by this process, so they have to be
let go deliberately: the freeze loop is its own process and the watches each have
a watcher in the worker to drop. The worker goes last, because stopping a watch
sends it one more command.
*/
func (u *ui) shutdown() {
	u.saveState()
	u.freeze.halt()
	for _, w := range []*watch{
		u.potions, u.fishing, u.buffs, u.catch, u.sell, u.inventory, u.statusPoll,
		u.projectiles,
	} {
		if w != nil {
			w.halt()
		}
	}
	u.work.Close()
}

/*
once runs a single operation in the background and logs what it said.

For the one-off commands beside a watch -- raising rod power as fishing starts,
restoring it as it stops, editing the sell list. Not through Perform: those run
while a watch is already going, and Perform refuses a second operation.
*/
func (u *ui) once(what string, argv []string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), onceTimeout)
		defer cancel()
		out, err := u.run(ctx, argv)
		fyne.Do(func() {
			if err != nil {
				u.note(what + " failed: " + firstLine(detail(out, err)))
				return
			}
			for _, line := range splitLines(out) {
				u.note(line)
			}
		})
	}()
}

// onceTimeout bounds a background one-off.
const onceTimeout = 60 * time.Second

/*
runUser performs one CLI operation without sudo.

For everything that does not touch the game's memory: the patch catalog, the
modifier list, building the icon cache, reading the recipes. Three reasons, and
each matters on its own.

The worker connects to a Service before it dispatches, so anything sent through
it needs Terraria running -- right for operations on the game and wrong for
static reads, which are made before anything is attached.

Least privilege: none of these needs root, so none of them asks for it.

And for the extractions it is load-bearing rather than tidy. They write the icon
cache under the user's home. Run under sudo they would write it as root, and the
unprivileged extractor could then never rewrite it -- the Qt panel has a
separate argv builder for exactly this, and the cache lives under ~/.cache
rather than ~/.config for the same reason.
*/
func (u *ui) runUser(ctx context.Context, argv []string) (string, error) {
	if u.cli == "" {
		return "", fmt.Errorf("%s is not on PATH", cliName)
	}
	cmd := exec.CommandContext(ctx, u.cli, argv...) //nolint:gosec // argv comes from the client package
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// prompt asks for something in a dialog, using the library's shape so every
// dialog in the window looks the same.
func (u *ui) prompt(title, confirm string, body fyne.CanvasObject, do func()) {
	dialogs.Prompt(u.sh.Window, title, confirm, body, do)
}

// note writes one line to the window's log. Safe from any goroutine.
func (u *ui) note(msg string) {
	if u.log != nil {
		u.log.Log(logpane.Info, msg)
	}
}

/*
loadStatus refreshes what the status bar reports.

Through the shell's loader rather than Perform: it is a section's data load, not
an operation, so it runs beside other work instead of being refused while
something else is in flight. The game not being there is not a failure worth a
banner -- it is the ordinary state before Terraria is started, and the status bar
already says so -- so this returns nil and reports in band.
*/
func (u *ui) loadStatus() {
	u.sh.Load("Reading the game...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.StatusArgv())
		fyne.Do(func() { u.takeStatus(out, err) })
		return nil
	})
}

/*
pollStatus keeps the status bar current, and is what the build gate hangs off.

Every two seconds, the cadence the Qt panel arrived at. It is not only a display:
this is the one poll that can notice the game being restarted into a different
build under a window that is already open, which is the case the gate exists for.
*/
func (u *ui) pollStatus() {
	u.statusPoll = newWatch(u, "status", statusEvery,
		client.StatusArgv,
		func(raw string) { u.takeStatus(raw, nil) })
	u.statusPoll.set(true)
}

// statusEvery is the poll cadence, from the Qt panel's own timer.
const statusEvery = 2 * time.Second

// takeStatus stores a status reply and acts on it. On the UI thread.
func (u *ui) takeStatus(out string, err error) {
	st, ok := client.ParseStatus(out)
	u.mu.Lock()
	was := u.statusMsg
	u.status, u.statusOK = st, ok
	u.statusMsg = ""
	if !ok {
		u.statusMsg = shortReason(out, err)
	}
	said := was == u.statusMsg
	u.mu.Unlock()
	// The full text goes to the log once, not on every poll: this runs every
	// couple of seconds and "Terraria is not running" is not news the second
	// time.
	if !ok && !said {
		u.note(firstLine(detail(out, err)))
	}
	u.sh.RedrawStatus()
	if ok {
		u.maybeGateBuild(st)
		u.noteWorld(st)
		u.maybeRestore(st)
	}
}

/*
shortReason is the status bar's verdict: a few words, because the bar is a row of
short facts and a sentence in it pushes everything else off the end.

The CLI's own message is the useful one and it is long -- "no running
Terraria.exe found. Is Terraria launched (Windows build under Proton)?" is good
advice in a log and unreadable in a status bar -- so the bar gets the verdict and
the log gets the advice.
*/
func shortReason(out string, err error) string {
	text := detail(out, err)
	switch {
	case strings.Contains(text, "no running Terraria"):
		return "not running"
	case strings.Contains(text, "not served here"):
		return "unavailable"
	case strings.Contains(text, "sudo"), strings.Contains(text, "password"):
		return "sudo refused"
	case text == "":
		return "unreadable"
	}
	return "unreadable"
}

// detail is the fullest description of a failed read: what the CLI printed if it
// printed anything, and the process error otherwise.
func detail(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// firstLine trims a subprocess failure to the part worth putting in a log. sudo and the CLI both say useful things in their first line and less
// useful things after it.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// statusSegments are the status bar's facts, laid out left to right by the
// shell. The progress indicator is the shell's own and is not one of these.
func (u *ui) statusSegments() []fyne.CanvasObject {
	u.mu.Lock()
	st, ok, msg := u.status, u.statusOK, u.statusMsg
	u.mu.Unlock()

	if !ok || st == nil {
		text, level := "not attached", fd.StatusWarn
		if msg != "" {
			text = msg
			level = fd.StatusBad
		}
		return []fyne.CanvasObject{
			widgets.Dim("game"), widgets.StatusText(text, level),
			widgets.Sep(), widgets.Dim("worker"), u.workerText(),
		}
	}

	name := "no player"
	if st.Name != nil && *st.Name != "" {
		name = *st.Name
	}
	return []fyne.CanvasObject{
		widgets.Dim("player"), widgets.StatusText(name, fd.StatusGood), widgets.Sep(),
		widgets.Dim("hp"), widgets.StatusText(pair(st.HP, st.MaxHP), hpStatus(st)), widgets.Sep(),
		widgets.Dim("mana"), widgets.StatusText(pair(st.Mana, st.MaxMana), fd.StatusInfo), widgets.Sep(),
		widgets.Dim("build"), widgets.StatusText(widgets.OrNone(buildKey(st), "unknown"), fd.StatusInfo),
		widgets.Sep(), widgets.Dim("worker"), u.workerText(),
	}
}

// workerText says whether the warm worker is up, because the difference between
// 2.5 ms and 2.7 s a request is worth being able to see.
func (u *ui) workerText() fyne.CanvasObject {
	if u.work.Available() {
		return widgets.StatusText("warm", fd.StatusGood)
	}
	if !u.sudoOK {
		return widgets.StatusText("no passwordless sudo", fd.StatusBad)
	}
	return widgets.StatusText("per-command", fd.StatusWarn)
}

// buildKey is the version+buildid identity an AOB is really pinned to, which is
// the thing worth showing rather than the version alone.
func buildKey(st *client.Status) string {
	if st.BuildID != "" && st.Build != "" {
		return st.Build
	}
	return st.Version
}

// pair renders "current/ceiling", with an em dash for anything the game did not
// say -- which is different from zero, and in a window about HP that difference
// matters.
func pair(cur, ceiling *int) string {
	c, m := "—", "—"
	if cur != nil {
		c = strconv.Itoa(*cur)
	}
	if ceiling != nil {
		m = strconv.Itoa(*ceiling)
	}
	return c + "/" + m
}

// hpStatus colours the HP pair: a player near death is worth noticing in a
// window whose whole job is changing that number.
func hpStatus(st *client.Status) fd.Status {
	if st.HP == nil || st.MaxHP == nil || *st.MaxHP == 0 {
		return fd.StatusInfo
	}
	switch frac := float64(*st.HP) / float64(*st.MaxHP); {
	case frac <= 0.25:
		return fd.StatusBad
	case frac <= 0.5:
		return fd.StatusWarn
	}
	return fd.StatusGood
}

// errNotANumber and errOutOfRange are what the numeric entries reject with.
// Named rather than inline so every field says the same thing the same way.
var errNotANumber = errors.New("whole number")

func errOutOfRange(lo, hi int) error {
	return fmt.Errorf("%d to %d", lo, hi)
}

// splitLines breaks CLI output into log lines, dropping the blank tail a
// subprocess usually leaves.
func splitLines(out string) []string {
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			keep = append(keep, line)
		}
	}
	return keep
}
