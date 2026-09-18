package gui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
