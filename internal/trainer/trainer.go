/*
Package trainer is the freeze engine: holding player values against the game
overwriting them.

Terraria recomputes many player fields every frame -- life on damage and regen,
the derived stats from equipment -- so a one-shot write reverts. A value that
has to stay put is held by a rewrite loop running faster than the game's sixty
frames a second. Writes go to every matched copy; the live one takes effect and
the inert snapshots ignore them.

Reloading the world makes the player addresses stale and reads start failing.
The loop notices that and relocates rather than dying.

Ported from terrariabonker/trainer.py (spec 051, step 6).
*/
package trainer

import (
	"context"
	"errors"
	"time"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/player"
)

// DefaultHz is how often the loop rewrites, chosen to beat the game's frame.
const DefaultHz = 200

/*
StaleRounds is how many failed passes mean the addresses died rather than the
read merely missing.

One is not enough: a single pass can fail while the game is between frames, and
relocating on that costs a full memory scan every time it happens.
*/
const StaleRounds = 3

// Mem is what the freezer reads and writes through.
type Mem interface {
	locate.Mem
	player.Mem
}

// ErrNoPlayer is what a freeze reports when there is nobody to freeze.
var ErrNoPlayer = errors.New("no player found to freeze")

// Freezer holds a chosen set of values against the game until it is stopped.
type Freezer struct {
	Mem      Mem
	Godmode  bool
	Mana     bool
	Hz       int
	Saves    int
	players  []*player.Player
	relocate func() int
}

// New is a freezer over a running game.
func New(mem Mem, godmode, mana bool, hz int) *Freezer {
	if hz <= 0 {
		hz = DefaultHz
	}
	return &Freezer{Mem: mem, Godmode: godmode, Mana: mana, Hz: hz}
}

// Players is the copies the loop is currently writing to.
func (f *Freezer) Players() int { return len(f.players) }

// Locate finds the player copies again, and is how many there are.
func (f *Freezer) Locate() int {
	if f.relocate != nil {
		return f.relocate()
	}
	blocks := locate.FindPlayers(f.Mem)
	f.players = make([]*player.Player, 0, len(blocks))
	for _, b := range blocks {
		f.players = append(f.players, player.New(f.Mem, b.LifeAddr))
	}
	return len(f.players)
}

/*
Tick is one pass over every copy. It reports whether anything could be read at
all, which is what tells a stale address from a value that simply did not need
writing.
*/
func (f *Freezer) Tick() bool {
	anyOK := false
	for _, p := range f.players {
		if f.Godmode {
			if mx, ok := p.StatLifeMax(); ok {
				anyOK = true
				if life, got := p.StatLife(); got && life != mx {
					p.SetLife(mx)
					f.Saves++
				}
			}
		}
		if f.Mana {
			if mx, ok := p.StatManaMax(); ok {
				anyOK = true
				if mana, got := p.StatMana(); got && mana != mx {
					p.SetMana(mx)
				}
			}
		}
	}
	return anyOK
}

/*
Run freezes until the duration elapses, or until the context is cancelled when
there is none.

A run with nobody to freeze is a failure rather than a loop doing nothing: the
person asked for a value to be held and it is not being held.
*/
func (f *Freezer) Run(ctx context.Context, seconds time.Duration, onStart func(int)) error {
	if len(f.players) == 0 && f.Locate() == 0 {
		return ErrNoPlayer
	}
	if onStart != nil {
		onStart(len(f.players))
	}
	period := time.Second / time.Duration(f.Hz)
	deadline := time.Now().Add(seconds)
	stale := 0
	for seconds <= 0 || time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // a cancelled context is not a failure: report what was done
		}
		if f.Tick() {
			stale = 0
		} else {
			stale++
			if stale >= StaleRounds {
				// The world was reloaded and the addresses died. If the game is
				// gone or still loading there is nothing to find yet, so wait
				// before asking again rather than scanning in a tight loop.
				if f.Locate() == 0 && !sleep(ctx, 500*time.Millisecond) {
					return nil
				}
				stale = 0
			}
		}
		if !sleep(ctx, period) {
			return nil
		}
	}
	return nil
}

// sleep waits, and reports whether it finished rather than being cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
