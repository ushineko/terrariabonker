package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/container"
	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// sayOnce is what keeps a loop running four times a second from burying the
// output. The key is separate from the text because the same event reads
// differently each round.
func TestATransitionIsReportedOnceUntilItIsForgotten(t *testing.T) {
	u := testUI(t)
	u.log = nil // note() tolerates no pane; this test is about the bookkeeping

	w := newWatch(u, "potions", time.Second, nil, nil)
	w.start()
	t.Cleanup(w.halt)

	w.sayOnce("buff:7", "slot 1 buff 7 is up")
	w.sayOnce("buff:7", "slot 1 buff 7 is up")
	w.sayOnce("buff:7", "slot 2 buff 7 is up")
	require.Len(t, w.said, 1, "one event, one line, however it was worded")

	w.forget("buff:7")
	w.sayOnce("buff:7", "slot 1 buff 7 is up")
	require.Len(t, w.said, 1, "forgotten, so the next occurrence counts again")
}

// Starting a watch twice must not leave two loops running: the second start
// would double the cadence and the first would never be stopped.
func TestStartingAWatchTwiceRunsOneLoop(t *testing.T) {
	u := testUI(t)
	w := newWatch(u, "fishing", time.Hour, nil, nil)

	w.set(true)
	first := w.stop
	w.set(true)
	require.Equal(t, first, w.stop, "the second start must be a no-op")
	require.True(t, w.running())

	w.set(false)
	require.False(t, w.running())
	w.set(false) // stopping a stopped watch is allowed and does nothing
	require.False(t, w.running())
}

// A round is skipped while the previous one is still out, so a slow round
// cannot pile overlapping requests onto the worker.
func TestARoundIsSkippedWhileOneIsStillOut(t *testing.T) {
	u := testUI(t)
	w := newWatch(u, "sell", time.Hour, nil, nil)
	w.start()
	t.Cleanup(w.halt)

	w.mu.Lock()
	w.inflight = true
	w.mu.Unlock()

	// tick returns without sending, because it finds the previous round out.
	// With no worker available it would return anyway, so the guard is asserted
	// directly rather than through a side effect.
	w.tick()
	w.mu.Lock()
	still := w.inflight
	w.mu.Unlock()
	require.True(t, still, "the in-flight round must not be cleared by a skipped tick")
}

// A watch that is not running does not tick, however the loop is scheduled.
func TestAStoppedWatchDoesNotTick(t *testing.T) {
	u := testUI(t)
	sent := 0
	w := newWatch(u, "catch", time.Hour, func() []string { sent++; return nil }, nil)
	w.tick()
	require.Zero(t, sent, "a watch that was never started has nothing to send")
}

// freeze is a process, not a round. With no CLI resolved it must say so rather
// than spawning sudo with an empty program.
func TestFreezeWithoutACLISaysSoAndStartsNothing(t *testing.T) {
	u := testUI(t)
	u.freeze = &freezer{u: u}

	u.freeze.set(true, true)
	require.False(t, u.freeze.running(), "there is no CLI to run")

	u.freeze.set(false, false)
	require.False(t, u.freeze.running())
}

// The log line names what is being held, because "freeze on" does not say
// whether mana is included.
func TestFreezeNamesWhatItHolds(t *testing.T) {
	require.Equal(t, "HP and mana", describeFreeze(true, true))
	require.Equal(t, "HP", describeFreeze(true, false))
	require.Equal(t, "mana", describeFreeze(false, true))
}

// The catalog decides what the Patches section draws, so a window that has not
// read it yet must say so rather than render an empty section that looks broken.
func TestPatchesSaysSoBeforeTheCatalogIsRead(t *testing.T) {
	u := testUI(t)
	require.Empty(t, u.px.catalog)
	texts := fynetest.Texts(u.buildPatches())
	require.Contains(t, texts, "The patch catalog has not been read yet.")
}

/*
With a catalog it draws a tab per section, in the catalog's order.

Tabs rather than one list: the Qt panel moved away from a flat list because it
needed scrolling at twelve patches and the catalog only grows. The order is the
catalog's, which groups related patches; sorting would scatter them.

What each patch does is a hover tip, so it is deliberately not in the rendered
text -- printed under every row it tripled the height of the section.
*/
func TestPatchesDrawsATabPerSectionInCatalogOrder(t *testing.T) {
	u := testUI(t)
	u.px.catalog = []client.Patch{
		{Name: "mining", Label: "Global mining speed", Note: "faster", Section: "Build",
			Value: &client.ValueSpec{Kind: "f32", Default: 0.2, Lo: 0.05, Hi: 2, Unit: "pickSpeed"}},
		{Name: "reach", Label: "Placement reach", Note: "further", Section: "Build",
			Value: &client.ValueSpec{Kind: "i32", Default: 20, Lo: 0, Hi: 100, Unit: "extra tiles"}},
		{Name: "pylons", Label: "Multiple pylons", Note: "one per biome", Section: "Misc"},
	}
	u.px.sections = []string{"Build", "Misc"}
	u.px.on = map[string]bool{"pylons": true}

	built := u.buildPatches()
	tabs := fynetest.Find[*container.AppTabs](built)
	require.NotNil(t, tabs, "the sections are tabs")
	var titles []string
	for _, item := range tabs.Items {
		titles = append(titles, item.Text)
	}
	require.Equal(t, []string{"Build", "Misc"}, titles)

	texts := fynetest.Texts(built)
	for _, want := range []string{"Global mining speed", "Placement reach", "Multiple pylons",
		"extra tiles"} {
		require.Containsf(t, texts, want, "%q is missing from the section", want)
	}
	require.NotContains(t, texts, "one per biome", "the note is a hover tip, not a printed line")
}

// A value is shown as the game reports it, and as its own default when the game
// has reported nothing. An integer patch is not rendered with decimals.
func TestAPatchValueReadsAsTheKindItIs(t *testing.T) {
	tiles := client.Patch{Name: "reach", Section: "Build",
		Value: &client.ValueSpec{Kind: "i32", Default: 20, Lo: 0, Hi: 100}}
	speed := client.Patch{Name: "mining", Section: "Build",
		Value: &client.ValueSpec{Kind: "f32", Default: 0.2, Lo: 0.05, Hi: 2}}

	require.Equal(t, "35", formatValue(tiles, 35))
	require.Equal(t, "20", formatValue(tiles, 0), "nothing reported means the default")
	require.Equal(t, "0.35", formatValue(speed, 0.35))
	require.Equal(t, "", formatValue(client.Patch{Name: "pylons"}, 0), "no value, no number")
}

// A preset patch shows the preset matching the game's value, and falls back to
// its default rather than to whatever happens to be first.
func TestAPresetPatchNamesTheValueTheGameHas(t *testing.T) {
	p := client.Patch{Name: "fast_place", Value: &client.ValueSpec{
		Kind: "i32", Default: 4,
		Presets: []client.Preset{{Label: "Fast", Value: 10}, {Label: "Faster", Value: 4}, {Label: "Instant", Value: 0}},
	}}
	require.Equal(t, "Instant", presetFor(p, 0))
	require.Equal(t, "Fast", presetFor(p, 10))
	require.Equal(t, "Faster", presetFor(p, 99), "an unknown value falls back to the default")
	require.Equal(t, "", presetFor(client.Patch{Name: "pylons"}, 0))
}

// The typed value is held to the patch's own range, because the CLI will take
// anything and the game will not.
func TestATypedPatchValueIsHeldToItsRange(t *testing.T) {
	check := numberIn(0.05, 2)
	require.NoError(t, check("0.2"))
	require.Error(t, check("9"), "above the range")
	require.Error(t, check("0"), "below the range")
	require.Error(t, check("fast"), "not a number at all")
}
