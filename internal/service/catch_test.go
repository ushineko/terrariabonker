package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Auto-catch, and the gates that keep it from pressing the use button at nothing.

Every timing here was paid for. The rod is still in its use animation for a few
frames after a pull, so a cast attempted immediately is dropped -- that cost one
wasted press per fish. A cast is only reported once a bobber appears, because
arming is not casting. And nothing is cast until a line has been seen in the
water, so the cheat follows somebody who is fishing and does nothing to one
standing at a lake holding a rod.
*/

// A round refuses to run when its cheat is not applied, rather than pressing
// nothing.
func TestCatchingWithoutTheCheat(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, nil)
	p := patchFor(t, mem)

	_, err := service.New(mem, -1).CatchTick(p, false, 0)
	require.Error(t, err, "a round ran with auto-use off")
	require.Contains(t, err.Error(), "not enabled")
}

/*
A round presses nothing when there is no line in the water and no fish.

This is the state somebody is in for most of a fishing session, and it is the one
where a press goes into whatever they are holding.
*/
func TestAnIdleRoundPressesNothing(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, nil)
	p := autoUsePatcher(t, mem)

	got, err := service.New(mem, -1).CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, got["events"], "something was pressed with nothing in the water")
	require.Zero(t, p.AutoUse().Presses(), "the stub was armed")
}

/*
A fish on the line is reeled in: one press, one reel.

The catch is reported from the bobber's own slot, which is what tells the player
what they got rather than that something happened.
*/
func TestABiteIsReeledIn(t *testing.T) {
	mem := plant()
	// A bobber with a fish on it: the line is out, the window is counting down,
	// and the catch slot holds an item type.
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, -3, 0}, localAI: [3]float32{0, 2290, 0}}})
	p := autoUsePatcher(t, mem)

	got, err := service.New(mem, -1).CatchTick(p, false, 50*time.Millisecond)
	require.NoError(t, err)

	events := got["events"].([]service.CatchEvent)
	require.Len(t, events, 1, "the fish was not reeled in")
	require.Equal(t, "reel", events[0].What)
	require.Equal(t, int32(2290), events[0].Catch, "a different catch was reported")
	require.Equal(t, 3, events[0].Slot)
}

/*
Nothing is cast until a line has been seen in the water.

The gate lives in the round rather than in the caller, because both callers need
it and a gate in only one of them is a cheat that behaves differently depending
on which you use.
*/
func TestNothingIsCastBeforeAPlayerHasFished(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, nil) // no line, and none ever seen
	plantHoldingRod(mem)
	p := autoUsePatcher(t, mem)
	svc := service.New(mem, -1)

	got, err := svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, got["events"], "a cast was attempted before the player had fished")

	// They cast. Now the gate is open, and a round with nothing in the water
	// will try to put the line back out.
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, 120, 0}, localAI: [3]float32{0, 400, 0}}})
	_, err = svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)

	plantProjectilesInto(mem, nil) // reeled in, nothing in the water
	got, err = svc.CatchTick(p, true, 50*time.Millisecond)
	require.NoError(t, err)
	require.NotEmpty(t, got["events"], "the gate did not open after a line was seen")
}

/*
Stopping forgets the gate, so switching off and on starts over.

A remembered "they have cast once" would have the next session casting before
they touched the rod.
*/
func TestStoppingForgetsTheCastGate(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, 120, 0}, localAI: [3]float32{0, 400, 0}}})
	plantHoldingRod(mem)
	p := autoUsePatcher(t, mem)
	svc := service.New(mem, -1)

	_, err := svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)

	svc.CatchStop(p)
	require.False(t, p.AutoUse().Armed(), "a promised press survived stopping")

	plantProjectilesInto(mem, nil)
	got, err := svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, got["events"], "the gate survived being stopped")
}

// An item with no template is said to have none, rather than reported as firing
// nothing.
func TestAnItemWithNoTemplateIsRefused(t *testing.T) {
	_, err := service.New(plant(), -1).ProjectileOf(99999)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no template")
}

/*
Overrides are re-applied continuously, because nothing about them persists.

The game builds each projectile from its own defaults and never reads a template,
so a sweep that ran once would hold nothing.
*/
func TestProjectileTickSweepsRepeatedly(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, []bobber{{slot: 3, ptype: 985, ai: [3]float32{0, 1, 0}}})
	svc := service.New(mem, -1)

	got, err := svc.ProjectileTick(map[int32]map[string]float64{
		985: {"scale": 2.0},
	}, 30*time.Millisecond)
	require.NoError(t, err)
	require.Greater(t, got["sweeps"].(int), 1, "the overrides were applied once and left")
	require.Positive(t, got["patched"].(int), "nothing was written")
}

// Nothing to apply is a round that does nothing at all.
func TestAnEmptyProjectileTick(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, nil)
	before := mem.Hex()

	got, err := service.New(mem, -1).ProjectileTick(map[int32]map[string]float64{
		985: {}, // named, but with nothing to set
	}, 0)
	require.NoError(t, err)
	require.Zero(t, got["sweeps"])
	require.Equal(t, before, mem.Hex(), "an empty round wrote something")
}

/*
Stopping forgets which projectiles have been seen.

Switching off and on again is the player saying "start over", and a remembered
sighting would deny a set-once field to a projectile already in flight.
*/
func TestStoppingForgetsSeenProjectiles(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, []bobber{{slot: 3, ptype: 985, ai: [3]float32{0, 1, 0}}})
	svc := service.New(mem, -1)
	overrides := map[int32]map[string]float64{985: {"timeLeft": 30000}}

	first, err := svc.ProjectileTick(overrides, 0)
	require.NoError(t, err)
	require.Positive(t, first["patched"].(int), "the set-once field was not applied")

	again, err := svc.ProjectileTick(overrides, 0)
	require.NoError(t, err)
	require.Zero(t, again["patched"], "a set-once field was applied twice")

	require.True(t, svc.ProjectileStop()["stopped"].(bool))
	after, err := svc.ProjectileTick(overrides, 0)
	require.NoError(t, err)
	require.Positive(t, after["patched"].(int), "stopping did not make the projectile new again")
}

/*
No cast while the rod is still in its use animation.

The game drops a press that arrives in those few frames after a pull, so without
the wait every catch cost a wasted press: the log read "tried to cast and no line
went out" followed by "cast the line", once per fish.
*/
func TestNoCastImmediatelyAfterAReel(t *testing.T) {
	mem := plant()
	plantHoldingRod(mem)
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, -3, 0}, localAI: [3]float32{0, 2290, 0}}})
	p := autoUsePatcher(t, mem)
	svc := service.New(mem, -1)

	// A round that reels the fish in, which is what starts the animation.
	got, err := svc.CatchTick(p, true, 50*time.Millisecond)
	require.NoError(t, err)
	require.Len(t, got["events"].([]service.CatchEvent), 1, "the fish was not reeled in")

	// Immediately afterwards, with nothing in the water, no cast is attempted.
	plantProjectilesInto(mem, nil)
	before := p.AutoUse().Presses()
	got, err = svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, got["events"], "a cast was attempted while the rod was still swinging")
	require.Equal(t, before, p.AutoUse().Presses(), "the stub was armed anyway")
}

/*
No cast while holding something that is not a rod.

The use button is not fishing-specific, so pressing it against a sword swings the
sword -- once a tick, for as long as the cheat is on.
*/
func TestNoCastWhileHoldingSomethingElse(t *testing.T) {
	mem := plant()
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, 120, 0}, localAI: [3]float32{0, 400, 0}}})
	plantHoldingRod(mem)
	p := autoUsePatcher(t, mem)
	svc := service.New(mem, -1)

	// They cast, which opens the gate.
	_, err := svc.CatchTick(p, true, 20*time.Millisecond)
	require.NoError(t, err)

	// Then they put the rod away and hold a pickaxe, and the line comes in.
	plantProjectilesInto(mem, nil)
	plantHoldingSlot(mem, 0)
	got, err := svc.CatchTick(p, true, 50*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, got["events"], "the use button was pressed against something that is not a rod")
}

/*
A fish on the line ends the round, and nothing else is pressed.

One tick presses at most once: either the bite is taken or a cast is tried, never
both.
*/
func TestARoundPressesAtMostOnce(t *testing.T) {
	mem := plant()
	plantHoldingRod(mem)
	plantProjectilesInto(mem, []bobber{{slot: 3, ai: [3]float32{0, -3, 0}, localAI: [3]float32{0, 2290, 0}}})
	p := autoUsePatcher(t, mem)

	got, err := service.New(mem, -1).CatchTick(p, true, 50*time.Millisecond)
	require.NoError(t, err)

	events := got["events"].([]service.CatchEvent)
	require.Len(t, events, 1, "a round did more than one thing")
	require.Equal(t, "reel", events[0].What, "the fish was not what it did")
}
