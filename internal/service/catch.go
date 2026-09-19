package service

import (
	"fmt"
	"time"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
Auto-catch: take the fish that is on the line, and put the line back out.

Everything here is one press of the use button through the auto-use stub. The
timings are not arbitrary -- each was paid for by a wasted press or a stream of
them.
*/

const (
	/*
		CastConfirm is how long to wait for a bobber after arming a cast before
		calling it a miss. A cast bobber exists within a frame or two; longer
		than this and the press did nothing.
	*/
	CastConfirm = 500 * time.Millisecond

	/*
		CastSettle is how long after reeling in before a cast is worth
		attempting.

		The rod is still in its use animation for a few frames after the pull and
		the game drops the press, so without this every catch cost a wasted
		press: the log read "tried to cast and no line went out" followed by
		"cast the line", once per fish.
	*/
	CastSettle = 450 * time.Millisecond

	/*
		ArmGrace and BiteGrace are grace past a tick's own budget for the two
		waits inside a reel: long enough for the stub to consume the arm flag,
		and for the pull to register a frame later.

		Named rather than added inline, so the deadlines in a round are findable
		in one place and a test can shorten them.
	*/
	ArmGrace = 200 * time.Millisecond
	// BiteGrace is the same grace, for the wait on a bite.
	BiteGrace = 500 * time.Millisecond
)

// ProjectileArray is the game's projectile array, located once and kept.
func (s *Service) ProjectileArray() (uint32, error) {
	if s.projArr != 0 {
		return s.projArr, nil
	}
	base, ok := s.StaticBase()
	if !ok {
		return 0, &Error{Message: "could not locate Main.projectile"}
	}
	arr, ok := projectile.Array(s.Mem, base)
	if !ok {
		return 0, &Error{Message: "could not locate Main.projectile"}
	}
	s.projArr = arr
	return arr, nil
}

// CatchEvent is one thing a round did.
type CatchEvent struct {
	What      string `json:"what"`
	Catch     int32  `json:"catch,omitempty"`
	Slot      int    `json:"slot,omitempty"`
	Confirmed *bool  `json:"confirmed,omitempty"`
}

/*
takeBite reels one fish in: arm the stub once, then wait the bite out.

Waiting matters as much as the arming. The pull sets the reeling flag a frame
later, so the bite still reads as live immediately afterwards -- without the wait
the same fish is armed for again and again, which is the polling behaviour the
stub exists to replace.
*/
func (s *Service) takeBite(p *patch.Patcher, arr uint32, bite projectile.Bobber, end time.Time) (CatchEvent, bool) {
	auto := p.AutoUse()
	if !auto.Arm() {
		return CatchEvent{}, false
	}
	for auto.Armed() && time.Now().Before(end.Add(ArmGrace)) {
		time.Sleep(2 * time.Millisecond)
	}
	s.lastReel = time.Now()
	for {
		if _, biting := projectile.FindBite(s.Mem, arr); !biting {
			break
		}
		if !time.Now().Before(end.Add(BiteGrace)) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return CatchEvent{What: "reel", Catch: bite.Catch(), Slot: bite.Slot}, true
}

/*
readyToRecast reports whether casting is wanted right now. Two guards, each
earned.

The last reel must have settled -- the rod is still in its use animation for a
few frames after a pull and the game drops the press, which cost one wasted press
per fish before it was waited out. And the rod must be in hand: the use button is
not fishing-specific, so casting against a sword swings the sword.

Separate from the cast itself because the answer decides *control flow*, not just
the outcome. When it is false the round keeps looking for a bite rather than
ending -- collapsing the two would quietly turn "not yet" into "stop looking".
*/
func (s *Service) readyToRecast() bool {
	if time.Since(s.lastReel) <= CastSettle {
		return false
	}
	inv, err := s.liveInventory()
	if err != nil {
		return false
	}
	return inv.HoldingRod()
}

/*
tryRecast puts the line back out.

A cast is only reported when a bobber actually appears -- arming is not casting,
and an earlier version took credit for the player's own casts by reporting the
arm. When nothing goes out the gate closes: the commonest reason is a swap to
something that is not a rod, and a cheat that keeps arming then swings that thing
once a tick. One stray press is a bug; a stream of them is a different program.
The player's next real cast reopens the gate.
*/
func (s *Service) tryRecast(p *patch.Patcher, arr uint32) (CatchEvent, bool) {
	if !p.AutoUse().Arm() {
		return CatchEvent{}, false
	}
	yes, no := true, false
	deadline := time.Now().Add(CastConfirm)
	for time.Now().Before(deadline) {
		if len(projectile.FindBobbers(s.Mem, arr)) > 0 {
			return CatchEvent{What: "cast", Confirmed: &yes}, true
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.seenCast = false
	return CatchEvent{What: "cast", Confirmed: &no}, true
}

/*
CatchTick takes any fish on the line, for up to a budget. One slice.

It polls fast inside the privileged worker rather than returning between samples:
a bite window lasts a second or two, and a round trip per sample would spend most
of it in transit. The window drives this from a timer, as vein watching is.

**One tick presses at most once**: either the bite is taken or a cast is tried,
never both, and the round ends as soon as either happens.

Recasting is **gated here rather than by the caller**: nothing is cast until a
line has been seen in the water, so the cheat follows somebody who is fishing and
does nothing at all to one standing at a lake holding a rod. The gate lives in
the round because both callers need it, and a gate in only one of them is a cheat
that behaves differently depending on which you use.
*/
func (s *Service) CatchTick(p *patch.Patcher, recast bool, budget time.Duration) (map[string]any, error) {
	if !p.IsEnabled("auto_use") {
		return nil, &Error{Message: "the auto-use cheat is not enabled"}
	}
	arr, err := s.ProjectileArray()
	if err != nil {
		return nil, err
	}
	events := []CatchEvent{}
	end := time.Now().Add(budget)

	for time.Now().Before(end) {
		bobbers := projectile.FindBobbers(s.Mem, arr)
		if !s.seenCast && len(bobbers) > 0 {
			s.seenCast = true // they have cast; a recast may follow
		}
		if bite, biting := projectile.FindBite(s.Mem, arr); biting {
			if event, ok := s.takeBite(p, arr, bite, end); ok {
				events = append(events, event)
			}
			break
		}
		if recast && s.seenCast && len(bobbers) == 0 && s.readyToRecast() {
			if event, ok := s.tryRecast(p, arr); ok {
				events = append(events, event)
			}
			break
		}
		time.Sleep(time.Second / 120)
	}
	return map[string]any{"events": events, "presses": p.AutoUse().Presses()}, nil
}

/*
CatchStop forgets the located array and the cast gate. Called when the cheat goes
off.

The gate is deliberately reset: switching off and on again is the player saying
"start over", and a remembered "they have cast once" would have the next session
casting before they touched the rod.
*/
func (s *Service) CatchStop(p *patch.Patcher) map[string]any {
	had := s.projArr != 0
	s.projArr, s.seenCast = 0, false
	if p.IsEnabled("auto_use") {
		// A press promised but not yet landed is not wanted.
		p.AutoUse().Disarm()
	}
	return map[string]any{"stopped": had}
}

/*
ProjectileOf is the projectile type an item fires, from its template.

That field is how a weapon somebody recognises maps to the projectile the editor
actually writes to. A weapon that fires none cannot be edited, which the caller
should say plainly rather than offering a control that does nothing.
*/
func (s *Service) ProjectileOf(itemType int32) (map[string]any, error) {
	addr, ok := s.TemplateAddr(itemType)
	if !ok {
		return nil, &Error{Message: fmt.Sprintf("no template for item type %d", itemType)}
	}
	shoot, _ := s.Mem.ReadI32(addr + uint32(layout.ItemShoot)) //nolint:gosec // a field offset
	return map[string]any{"item": itemType, "shoot": shoot}, nil
}

/*
ProjectileTick holds overrides on live projectiles for up to a budget. One slice.

Driven from a timer the way vein watching and auto-catch are, and for the same
reason: the values have to be re-applied continuously, because the game never
reads a template when it builds a projectile. Returning between sweeps would
spend the interval in transit rather than in the game.

Nothing here persists. Switching the cheat off restores nothing and needs to
restore nothing -- the next projectile the game spawns is a fresh object built
from its own defaults.
*/
func (s *Service) ProjectileTick(overrides map[int32]map[string]float64, budget time.Duration) (map[string]any, error) {
	clean := map[int32]map[string]float64{}
	for ptype, fields := range overrides {
		if len(fields) > 0 {
			clean[ptype] = fields
		}
	}
	if len(clean) == 0 {
		return map[string]any{"patched": 0, "types": map[int32]int{}, "sweeps": 0}, nil
	}
	arr, err := s.ProjectileArray()
	if err != nil {
		return nil, err
	}
	if s.projEditor == nil {
		s.projEditor = projectile.NewEditor()
	}
	if budget < 0 {
		budget = 0
	}
	end := time.Now().Add(budget)

	patched, sweeps := 0, 0
	types := map[int32]int{}
	for {
		out := s.projEditor.Sweep(s.Mem, arr, clean)
		patched += out.Patched
		sweeps++
		for t, n := range out.Types {
			types[t] += n
		}
		if !time.Now().Before(end) {
			break
		}
		time.Sleep(time.Second / 120)
	}
	return map[string]any{"patched": patched, "types": types, "sweeps": sweeps}, nil
}

/*
ProjectileStop forgets per-projectile state. Called when the cheat goes off.

Deliberately forgets which projectiles have been seen: switching off and on again
is the player saying "start over", and a remembered sighting would deny a
set-once field to a projectile already in flight.
*/
func (s *Service) ProjectileStop() map[string]any {
	had := s.projEditor != nil
	s.projEditor = nil
	return map[string]any{"stopped": had}
}
