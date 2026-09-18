package gui

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
freezer holds values against the game: godmode pins HP, and infinite mana pins
mana.

Unlike everything else on the Effects section this is not a round the window
ticks. `freeze` is a blocking loop in the CLI, so it runs as its own process and
is stopped by killing it. It cannot go through the warm worker for the same
reason the ticks cannot use --watch: a blocking command would stop the worker
answering anything else.

Both switches drive one process. Turning either on restarts it with the flags
that are wanted now, because the loop takes them at start-up and there is
nothing to talk to once it is running.
*/
type freezer struct {
	u *ui

	mu   sync.Mutex
	cmd  *exec.Cmd
	stop context.CancelFunc
}

// freezeStopGrace is how long a freeze loop is given to notice it has been
// asked to stop before it is killed outright.
const freezeStopGrace = 1500 * time.Millisecond

// set restarts the freeze loop for the flags given, or stops it when neither is
// wanted.
func (f *freezer) set(godmode, mana bool) {
	if f == nil {
		return
	}
	f.halt()
	if !godmode && !mana {
		f.u.note("[freeze] stopped")
		return
	}
	if f.u.cli == "" || !f.u.sudoOK {
		f.u.note("[freeze] needs the CLI and passwordless sudo")
		return
	}

	argv := client.FreezeArgv(godmode, mana)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sudo", append(sudoPrefix(f.u.cli), argv...)...) //nolint:gosec // argv comes from the client package
	// Kill rather than wait if it will not go: this is a loop with no stdin to
	// close, and the window must not hang on it at shutdown.
	cmd.WaitDelay = freezeStopGrace
	if err := cmd.Start(); err != nil {
		cancel()
		f.u.note("[freeze] did not start: " + err.Error())
		return
	}

	f.mu.Lock()
	f.cmd, f.stop = cmd, cancel
	f.mu.Unlock()
	f.u.note("[freeze] holding " + describeFreeze(godmode, mana))

	// Reaped here so a loop that dies on its own -- the game closed, the player
	// unloaded -- is reported rather than leaving the switches claiming it is up.
	go func() {
		err := cmd.Wait()
		cancel()
		f.mu.Lock()
		current := f.cmd == cmd
		if current {
			f.cmd, f.stop = nil, nil
		}
		f.mu.Unlock()
		if current && err != nil && ctx.Err() == nil {
			f.u.note("[freeze] stopped on its own: " + err.Error())
		}
	}()
}

// running reports whether a freeze loop is up. Nil-safe, as the watches are.
func (f *freezer) running() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cmd != nil
}

// halt stops the loop if one is running.
func (f *freezer) halt() {
	if f == nil {
		return
	}
	f.mu.Lock()
	stop := f.stop
	f.cmd, f.stop = nil, nil
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// describeFreeze names what is being held, for the log.
func describeFreeze(godmode, mana bool) string {
	switch {
	case godmode && mana:
		return "HP and mana"
	case godmode:
		return "HP"
	default:
		return "mana"
	}
}
