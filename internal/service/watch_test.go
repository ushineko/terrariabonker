package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
The blocking loops, which are what the command line runs and the window does
not.

Each one is a loop around a tick the window drives from a timer, so almost
nothing here decides anything -- which is the thing worth checking: that a loop
counts what its tick did rather than deciding for itself, and that it can be
stopped.
*/

// A fixed number of potion rounds does the same thing on both sides.
func TestWatchingPotionsMatchesThePython(t *testing.T) {
	atHome(t)
	var want struct {
		Result map[string]any `json:"result"`
		Buf    string         `json:"buf"`
	}
	askPython(t, preamble()+plantBuffs()+`
res = svc.watch_potions(min_stack=1, interval=0.001, rounds=3)
print(json.dumps({"result": res, "buf": mem.buf.hex()}))`, &want)

	mem := plant()
	plantBuffsInto(mem)
	got, err := service.New(mem, -1).WatchPotions(t.Context(), 1, 0,
		time.Millisecond, 3, nil)
	require.NoError(t, err)
	require.Equal(t, want.Result, asJSON(t, got), "a different run of the watcher")
	sameMemory(t, want.Buf, mem.Hex(), "the two left different memory behind")
	require.Equal(t, 3, got.Rounds, "a different number of rounds was run")
}

/*
An interval that cannot hold a buff up is refused before the first round.

The failure otherwise is a buff that flickers, which reads as the trainer being
broken rather than as a setting being wrong.
*/
func TestAnIntervalThatCannotHoldABuffUp(t *testing.T) {
	atHome(t)
	var want string
	askPython(t, preamble()+plantBuffs()+`
try:
    svc.watch_potions(interval=10.0, rounds=1)
    print(json.dumps(""))
except Exception as e:
    print(json.dumps(str(e)))`, &want)
	require.NotEmpty(t, want, "the Python ran a watcher that cannot work")

	mem := plant()
	plantBuffsInto(mem)
	_, err := service.New(mem, -1).WatchPotions(t.Context(), 1, 0, 10*time.Second, 1, nil)
	require.Error(t, err, "an interval longer than the buff was accepted")
	require.Contains(t, err.Error(), "would lapse between rounds")
}

// And the same rule for the fishing buffs, which are the same length.
func TestAnIntervalThatCannotHoldAFishingBuffUp(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)
	_, err := service.New(mem, -1).WatchFishingBuffs(t.Context(),
		map[string]bool{"power": true}, 0, 10*time.Second, 1, nil)
	require.Error(t, err, "an interval longer than the buff was accepted")
}

// Topping bait up in a loop reports what the ticks did.
func TestWatchingBaitMatchesThePython(t *testing.T) {
	atHome(t)
	var want struct {
		Result map[string]any `json:"result"`
		Buf    string         `json:"buf"`
	}
	askPython(t, preamble()+pyPlantBait()+`
res = svc.watch_bait(keep=30, interval=0.001, rounds=2)
print(json.dumps({"result": res, "buf": mem.buf.hex()}))`, &want)

	mem := plant()
	plantBaitInto(mem)
	var seen []service.Tick
	got, err := service.New(mem, -1).WatchBait(t.Context(), 30, time.Millisecond, 2,
		func(tick service.Tick) { seen = append(seen, tick) })
	require.NoError(t, err)
	require.Equal(t, want.Result, asJSON(t, got), "a different run of the watcher")
	sameMemory(t, want.Buf, mem.Hex(), "the two left different memory behind")

	/*
		And only the round that topped something up was reported.

		The second round has nothing left to do, and a watcher that announced it
		would put a refill on the screen every second for as long as it ran.
	*/
	require.Len(t, seen, 1, "a round that topped nothing up was reported")
	require.NotEmpty(t, seen[0]["topped"], "the round that was reported did nothing")
}

/*
Selling in a loop counts what its rounds sold.

Go-only, like the selling round underneath it: what goes in the piggy bank is
checked against the fixture rather than against the other implementation,
because the fixture is what says which coins are the right coins.
*/
func TestWatchingSellingCountsWhatItSold(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	plantSellableInto(mem)

	var seen []service.Tick
	got, err := service.New(mem, -1).WatchSelling(t.Context(), time.Millisecond, 2,
		func(tick service.Tick) { seen = append(seen, tick) })
	require.NoError(t, err)
	require.Equal(t, 2, got.Rounds, "a different number of rounds was run")
	require.Positive(t, got.Copper, "two rounds of selling earned nothing")

	/*
		And only the round that sold something was reported.

		The second round has nothing left to sell, and a watcher that announced
		it would put an empty sale on the screen every half second for as long as
		it ran.
	*/
	require.Len(t, seen, 1, "a round that sold nothing was reported")
	require.NotEmpty(t, seen[0]["sold"], "the round that was reported sold nothing")
}

/*
A cancelled loop stops at once rather than waiting out its interval.

These are what a person stops by closing the window or pressing ^C, and an
interval measured in seconds is a wait they did not ask for.
*/
func TestACancelledWatcherStopsAtOnce(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	started := time.Now()
	got, err := service.New(mem, -1).WatchPotions(ctx, 1, 0, time.Second, 0, nil)
	require.NoError(t, err)
	require.Zero(t, got.Rounds, "a cancelled watcher ran a round anyway")
	require.Less(t, time.Since(started), time.Second, "it waited out the interval")
}

// And one cancelled mid-run stops after the round it is in.
func TestAWatcherStopsWhenItIsCancelled(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan service.PotionRound, 1)
	go func() {
		got, _ := service.New(mem, -1).WatchPotions(ctx, 1, 0, time.Millisecond, 0, nil)
		done <- got
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		require.Positive(t, got.Rounds, "it stopped before running anything")
		require.Positive(t, got.Applied, "nothing was reported as applied")
	case <-time.After(10 * time.Second):
		require.Fail(t, "the watcher did not stop when it was cancelled")
	}
}
