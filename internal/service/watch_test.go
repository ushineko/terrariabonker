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

// And the same rule for the fishing buffs, which are the same length.
func TestAnIntervalThatCannotHoldAFishingBuffUp(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)
	_, err := service.New(mem, -1).WatchFishingBuffs(t.Context(),
		map[string]bool{"power": true}, 0, 10*time.Second, 1, nil)
	require.Error(t, err, "an interval longer than the buff was accepted")
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
