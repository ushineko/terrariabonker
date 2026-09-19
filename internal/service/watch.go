package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ushineko/terrariabonker/internal/buffs"
	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
The blocking watch loops.

Each is a loop around a tick the window also drives from a timer, so the two
share one implementation and behave the same way -- every rule about when to act
lives in the tick, and nothing here decides anything.

They stop on the context rather than only on a round count, because they are
what a person stops by closing the window or pressing ^C, and waiting out the
interval first is a second of nothing happening.
*/

// Tick is one round's report, in the loose shape the ticks return.
type Tick = map[string]any

/*
CatchRound is what a run of auto-catch took.

Casts that produced no bobber are counted apart from those that did: the press
is what the trainer controls and the bobber is what the game did with it, so a
run that cast fifty times and confirmed none is a different failure from one
that never cast.
*/
type CatchRound struct {
	Rounds     int `json:"rounds"`
	Caught     int `json:"caught"`
	Cast       int `json:"cast"`
	CastMissed int `json:"cast_missed"`
}

// WatchCatch keeps fishing. The recast gate lives in CatchTick, so this and the
// panel behave the same way.
func (s *Service) WatchCatch(ctx context.Context, p *patch.Patcher, recast bool,
	budget, interval time.Duration, rounds int, onEvent func(CatchEvent)) (CatchRound, error) {
	out := CatchRound{}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := s.CatchTick(p, recast, budget)
		if err != nil {
			return out, err
		}
		events, _ := got["events"].([]CatchEvent)
		for _, e := range events {
			switch {
			case e.What == "reel":
				out.Caught++
			case e.Confirmed != nil && *e.Confirmed:
				out.Cast++
			default:
				out.CastMissed++
			}
			if onEvent != nil {
				onEvent(e)
			}
		}
		if !sleepCtx(ctx, interval) {
			return out, nil
		}
	}
	return out, nil
}

// BaitRound is what a run of the bait watcher topped up.
type BaitRound struct {
	Rounds  int `json:"rounds"`
	Refills int `json:"refills"`
}

// WatchBait keeps bait topped up.
func (s *Service) WatchBait(ctx context.Context, keep int32, interval time.Duration,
	rounds int, onEvent func(Tick)) (BaitRound, error) {
	out := BaitRound{}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := s.BaitTick(keep)
		if err != nil {
			return out, err
		}
		if topped, _ := got["topped"].([]Topped); len(topped) > 0 {
			out.Refills += len(topped)
			if onEvent != nil {
				onEvent(got)
			}
		}
		if !sleepCtx(ctx, interval) {
			return out, nil
		}
	}
	return out, nil
}

// SellRound is what a run of the selling watcher earned.
type SellRound struct {
	Rounds int   `json:"rounds"`
	Copper int32 `json:"copper"`
}

// WatchSelling keeps selling as items arrive.
func (s *Service) WatchSelling(ctx context.Context, interval time.Duration, rounds int,
	onEvent func(Tick)) (SellRound, error) {
	out := SellRound{}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := s.SellTick(false)
		if err != nil {
			return out, err
		}
		sold, _ := got["sold"].([]Sold)
		failed, _ := got["error"].(string)
		if len(sold) > 0 || failed != "" {
			copper, _ := got["copper"].(int32)
			out.Copper += copper
			if onEvent != nil {
				onEvent(got)
			}
		}
		if !sleepCtx(ctx, interval) {
			return out, nil
		}
	}
	return out, nil
}

// BuffRound is how many rounds a buff watcher ran for.
type BuffRound struct {
	Rounds int `json:"rounds"`
}

/*
WatchFishingBuffs keeps the fishing effects up.

An interval longer than the buff itself cannot hold anything up, so it is
refused rather than run: the buff lapses between rounds and the player sees it
flicker, which looks like the trainer not working.
*/
func (s *Service) WatchFishingBuffs(ctx context.Context, want map[string]bool, ticks int32,
	interval time.Duration, rounds int, onEvent func(Tick)) (BuffRound, error) {
	if err := holdsUp(interval, buffs.DefaultTicks); err != nil {
		return BuffRound{}, err
	}
	out := BuffRound{}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := s.FishingBuffTick(want, ticks)
		if err != nil {
			return out, err
		}
		if onEvent != nil {
			onEvent(got)
		}
		if !sleepCtx(ctx, interval) {
			return out, nil
		}
	}
	return out, nil
}

// PotionRound is how many buffs a run of the potion watcher put on.
type PotionRound struct {
	Rounds  int `json:"rounds"`
	Applied int `json:"applied"`
}

// WatchPotions keeps the buffs of favorited potions renewed.
func (s *Service) WatchPotions(ctx context.Context, minStack, ticks int32,
	interval time.Duration, rounds int, onEvent func(Tick)) (PotionRound, error) {
	want := ticks
	if want <= 0 {
		want = buffs.DefaultTicks
	}
	if err := holdsUp(interval, want); err != nil {
		return PotionRound{}, err
	}
	out := PotionRound{}
	for rounds <= 0 || out.Rounds < rounds {
		if ctx.Err() != nil {
			return out, nil
		}
		out.Rounds++
		got, err := s.PotionTick(minStack, want)
		if err != nil {
			return out, err
		}
		added, _ := got[buffs.Added].([]map[string]int32)
		renewed, _ := got[buffs.Renewed].([]map[string]int32)
		if len(added)+len(renewed) > 0 {
			out.Applied += len(added) + len(renewed)
			if onEvent != nil {
				onEvent(got)
			}
		}
		if !sleepCtx(ctx, interval) {
			return out, nil
		}
	}
	return out, nil
}

/*
holdsUp refuses an interval that cannot keep a buff of a given length up.

Checked before the first round rather than discovered by watching the buff bar:
the failure is a buff that flickers, which reads as the trainer being broken
rather than as a setting being wrong.
*/
func holdsUp(interval time.Duration, ticks int32) error {
	if interval.Seconds()*60 >= float64(ticks) {
		return &Error{Message: fmt.Sprintf(
			"a %gs interval cannot hold a %d-tick buff up -- "+
				"the buff would lapse between rounds", interval.Seconds(), ticks)}
	}
	return nil
}
