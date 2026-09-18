package gui

import (
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

/*
watch is one trainer-held loop: a cheat the window keeps up by sending the
worker a round at a fixed cadence.

Six of the Effects controls are these. None uses the CLI's own --watch form,
because a blocking loop in the worker would stop it answering anything else, so
the window owns the cadence and sends one round per tick.

Three rules the Qt panel arrived at, kept here because each was a bug first:

  - A round is skipped while the previous one is still out. Without that, a slow
    round piles overlapping requests onto the worker and they queue up behind
    each other for as long as the cheat is on.
  - Rounds go through the warm worker only, never the one-shot fallback. At four
    rounds a second a 2.7 s fallback would not merely be slow; it would never
    finish before the next tick.
  - Only transitions are logged. These run several times a second, and a line
    per round buries everything else in the output within a minute.
*/
type watch struct {
	// name is what the log calls it, in brackets: "[potions] ...".
	name string
	// every is the cadence. It has to stay well under whatever the round writes
	// -- a buff renewed every 250 ms lapses visibly if the tick slows to the
	// buff's own duration.
	every time.Duration
	// argv is built per round rather than once, because the controls it reads
	// can be changed while the watch is running.
	argv func() []string
	// round handles one reply, on the UI thread.
	round func(raw string)
	// stopArgv, when set, is sent once when the watch is switched off: a
	// watcher left armed in the worker would keep acting after the box is
	// unticked.
	stopArgv func() []string

	u        *ui
	mu       sync.Mutex
	stop     chan struct{}
	inflight bool
	// said remembers what has already been reported, so a transition is logged
	// once rather than every round.
	said map[string]bool
}

// newWatch describes a loop. It does not start it.
func newWatch(u *ui, name string, every time.Duration, argv func() []string, round func(string)) *watch {
	return &watch{u: u, name: name, every: every, argv: argv, round: round, said: map[string]bool{}}
}

// onStop registers the argv sent once when the watch goes off.
func (w *watch) onStop(argv func() []string) *watch {
	w.stopArgv = argv
	return w
}

// running reports whether the loop is up. Nil-safe: a section can be built
// before the loops exist, and a crash on launch is a poor way to learn that.
func (w *watch) running() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stop != nil
}

// set starts or stops the loop, and does nothing if it is already in that state.
func (w *watch) set(on bool) {
	if w == nil {
		return
	}
	if on {
		w.start()
		return
	}
	w.halt()
}

func (w *watch) start() {
	w.mu.Lock()
	if w.stop != nil {
		w.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	w.stop = stop
	w.said = map[string]bool{}
	w.mu.Unlock()

	go func() {
		t := time.NewTicker(w.every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				w.tick()
			}
		}
	}()
}

func (w *watch) halt() {
	w.mu.Lock()
	stop := w.stop
	w.stop, w.inflight = nil, false
	w.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)

	if w.stopArgv == nil {
		return
	}
	// Told to drop its watcher, off the UI thread: switching a box off must not
	// wait on a round trip.
	argv := w.stopArgv()
	go func() { _, _ = w.u.work.Do(argv) }()
}

// tick sends one round, unless the previous one is still out or the worker is
// not there to take it.
func (w *watch) tick() {
	if !w.u.work.Available() {
		return
	}
	w.mu.Lock()
	if w.inflight || w.stop == nil {
		w.mu.Unlock()
		return
	}
	w.inflight = true
	w.mu.Unlock()

	out, err := w.u.work.Do(w.argv())

	w.mu.Lock()
	w.inflight = false
	stopped := w.stop == nil
	w.mu.Unlock()
	// A reply that arrived after the box was unticked is not acted on: the
	// section has already been told the cheat is off.
	if stopped {
		return
	}
	if err != nil {
		w.sayOnce("err:"+err.Error(), err.Error())
		return
	}
	fyne.Do(func() { w.round(out) })
}

/*
sayOnce logs a line the first time its key is seen and stays quiet after.

key is separate from the text because the same event reads differently each
round -- "bait topped to 30" then "to 29" -- and what deserves one line is the
event, not each wording of it.
*/
func (w *watch) sayOnce(key, text string) {
	w.mu.Lock()
	if w.said[key] {
		w.mu.Unlock()
		return
	}
	w.said[key] = true
	w.mu.Unlock()
	w.u.note("[" + w.name + "] " + text)
}

// say logs a line every time. For transitions that are already rare.
func (w *watch) say(text string) { w.u.note("[" + w.name + "] " + text) }

// forget drops a remembered key, so the next occurrence is reported again. Used
// where a stack empties and refilling it is worth hearing about a second time.
func (w *watch) forget(key string) {
	w.mu.Lock()
	delete(w.said, key)
	w.mu.Unlock()
}
