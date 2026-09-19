package service

import (
	"fmt"
	"sort"

	"github.com/ushineko/terrariabonker/internal/buffs"
	"github.com/ushineko/terrariabonker/internal/profile"
)

/*
Fishing: the gear, the rods' power, the bait, and the potions held up without
potions.
*/

/*
What the kit hands out.

The best rod in the game and the strongest common bait -- and bait power does
double duty, because a higher power also lowers how often a bait is consumed.
*/
const (
	KitRod       = 2294
	KitBait      = 2676
	KitBaitStack = 30
)

// Gear is what a player is carrying, and what was given to them.
type Gear struct {
	Gave  map[string]Given `json:"gave"`
	Rods  []RodRow         `json:"rods"`
	Baits []BaitRow        `json:"baits"`
}

// Given is one thing the kit handed out.
type Given struct {
	Type  int32 `json:"type"`
	Slot  int   `json:"slot"`
	Stack int32 `json:"stack,omitempty"`
}

// RodRow and BaitRow are what the player carries.
type (
	RodRow struct {
		Slot  int  `json:"slot"`
		Power byte `json:"power"`
	}
	BaitRow struct {
		Slot  int   `json:"slot"`
		Power byte  `json:"power"`
		Stack int32 `json:"stack"`
	}
)

/*
FishingKit gives a rod and bait to a player who has neither, and does nothing
twice.

Deliberately nothing for a player who already has gear: a cheat that handed out
another rod every time it was switched on would fill the inventory, and the rod
somebody chose is likelier to be the one they want.
*/
func (s *Service) FishingKit() (Gear, error) {
	inv, err := s.liveInventory()
	if err != nil {
		return Gear{}, err
	}
	out := Gear{Gave: map[string]Given{}}
	have := inv.FishingGear()

	if len(have.Rods) == 0 {
		slot, err := s.GiveItem(KitRod, 1)
		if err != nil {
			return Gear{}, err
		}
		out.Gave["rod"] = Given{Type: KitRod, Slot: slot}
	}
	if len(have.Baits) == 0 {
		slot, err := s.GiveItem(KitBait, KitBaitStack)
		if err != nil {
			return Gear{}, err
		}
		out.Gave["bait"] = Given{Type: KitBait, Slot: slot, Stack: KitBaitStack}
	}

	after, err := s.liveInventory()
	if err != nil {
		return Gear{}, err
	}
	now := after.FishingGear()
	out.Rods, out.Baits = []RodRow{}, []BaitRow{}
	for _, r := range now.Rods {
		out.Rods = append(out.Rods, RodRow{Slot: r.Slot, Power: r.Power})
	}
	for _, b := range now.Baits {
		out.Baits = append(out.Baits, BaitRow{Slot: b.Slot, Power: b.Power, Stack: b.Stack})
	}
	return out, nil
}

/*
MaxFishingPower is the ceiling on the tunable.

The published bite *chance* stops improving well below this, but the counter that
decides how often a bite is rolled scales with power and is not obviously capped
-- the maximum measurably out-fished what a lower cap would allow. It is also the
field's own limit: a byte, so one more would wrap to nothing.
*/
const MaxFishingPower = 255

// PowerChange is one rod that was raised.
type PowerChange struct {
	Slot int   `json:"slot"`
	Type int32 `json:"type"`
	Was  byte  `json:"was"`
	Now  int32 `json:"now"`
}

/*
SetFishingPower raises every rod carried, recording what each was first.

The original goes to disk **before** the new value is written, not after: if this
process dies between the two, a missing record loses the rod's real power forever
while a spare record only causes a harmless restore.
*/
func (s *Service) SetFishingPower(power int32) (map[string]any, error) {
	if power < 1 || power > MaxFishingPower {
		return nil, &Error{Message: fmt.Sprintf("fishing power must be 1..%d", MaxFishingPower)}
	}
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	changed := []PowerChange{}
	for _, rod := range inv.FishingGear().Rods {
		if int32(rod.Power) == power {
			continue
		}
		slot, ok := inv.ReadSlot(rod.Slot)
		if !ok {
			continue
		}
		if err := profile.RememberRodPower(slot.Type, int32(rod.Power)); err != nil {
			return nil, err
		}
		inv.SetFishingPower(rod.Slot, power)
		changed = append(changed, PowerChange{
			Slot: rod.Slot, Type: slot.Type, Was: rod.Power, Now: power,
		})
	}
	return map[string]any{"power": power, "changed": changed}, nil
}

// Restored is one rod put back.
type Restored struct {
	Slot  int   `json:"slot"`
	Type  int32 `json:"type"`
	Power int32 `json:"power"`
}

/*
RestoreFishingPower puts every rod back to the power it had, and is safe to call
when nothing is owed.

Restores by item **type**, not slot: a rod that has been moved since the cheat
was switched on is still the same rod, and a slot-keyed restore loses track of
exactly that.
*/
func (s *Service) RestoreFishingPower() (map[string]any, error) {
	owed := profile.RodPowersToRestore()
	if len(owed) == 0 {
		return map[string]any{"restored": []Restored{}}, nil
	}
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	done := []Restored{}
	for _, rod := range inv.FishingGear().Rods {
		slot, ok := inv.ReadSlot(rod.Slot)
		if !ok {
			continue
		}
		power, due := owed[slot.Type]
		if !due {
			continue
		}
		inv.SetFishingPower(rod.Slot, power)
		done = append(done, Restored{Slot: rod.Slot, Type: slot.Type, Power: power})
	}
	/*
		Every owed rod is forgotten, including one not carried right now: keeping
		it would re-apply an old power to a rod picked up again much later.
	*/
	forgotten := make([]int, 0, len(owed))
	for itemType := range owed {
		if err := profile.ForgetRodPower(itemType); err != nil {
			return nil, err
		}
		forgotten = append(forgotten, int(itemType))
	}
	sort.Ints(forgotten)
	return map[string]any{"restored": done, "forgotten": forgotten}, nil
}

// Topped is one bait stack that was filled back up.
type Topped struct {
	Slot  int   `json:"slot"`
	Power byte  `json:"power"`
	Was   int32 `json:"was"`
	Now   int32 `json:"now"`
}

/*
BaitTick tops any bait stack below a floor back up to it. One round.

Stateless, like the potion round and for the same reason: the command line runs
it in a fresh process each time, so anything remembered between rounds would
exist for the window and not for the command line. "Keep at least this many"
needs no memory and says exactly what it does.
*/
func (s *Service) BaitTick(keep int32) (map[string]any, error) {
	if keep < 1 {
		return nil, &Error{Message: "keep must be at least 1"}
	}
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	invs, err := s.allInventories()
	if err != nil {
		return nil, err
	}
	topped := []Topped{}
	for _, bait := range inv.FishingGear().Baits {
		if bait.Stack >= keep {
			continue
		}
		for _, i := range invs {
			i.SetStack(bait.Slot, keep)
		}
		topped = append(topped, Topped{
			Slot: bait.Slot, Power: bait.Power, Was: bait.Stack, Now: keep,
		})
	}
	return map[string]any{
		"keep": keep, "topped": topped,
		"baits": len(inv.FishingGear().Baits),
	}, nil
}

/*
FishingBuffs is the three fishing potions and the effects they grant.

Read out of the game's own item templates rather than guessed. Holding these is
the whole cheat: a buff is a type and a time the game counts down, so renewing
one is exactly what drinking a potion does.
*/
var FishingBuffs = []struct {
	Key  string
	Buff int32
	Name string
	What string
}{
	{"power", 121, "Fishing Potion", "fishing power +15"},
	{"sonar", 122, "Sonar Potion", "see what is biting"},
	{"crate", 123, "Crate Potion", "more crates"},
}

// HeldBuff is one effect this round dealt with.
type HeldBuff struct {
	Effect string `json:"effect"`
	Buff   int32  `json:"buff"`
	Name   string `json:"name"`
	What   string `json:"what"`
}

/*
FishingBuffTick holds the chosen fishing-potion effects up, without the potions.
One round.

**Anything already running for longer is left completely alone.** That is what
makes a real potion, and the passive-potions cheat, take precedence: a renewal
refuses to shorten a buff, so somebody who drank an eight-minute potion keeps
eight minutes rather than being cut to the couple of seconds this renews on. It
never removes a buff either, so switching it off drops only what it was holding
up.
*/
func (s *Service) FishingBuffTick(want map[string]bool, ticks int32) (map[string]any, error) {
	held, deferred := []HeldBuff{}, []HeldBuff{}
	if len(want) == 0 {
		return map[string]any{"held": held, "deferred": deferred}, nil
	}
	live, err := s.LiveBlock()
	if err != nil {
		return nil, err
	}
	bar := buffs.New(s.Mem, live.LifeAddr)
	if ticks <= 0 {
		ticks = buffs.DefaultTicks
	}

	for _, effect := range FishingBuffs {
		if !want[effect.Key] {
			continue
		}
		/*
			A renewal reports "kept" both when something else owns the buff and
			when this loop's own renewal has not run out yet, and those are
			different things to tell somebody: the first is deferring to a potion,
			the second is the loop doing its job. Only a buff running *longer*
			than this would ever set belongs to somebody else.
		*/
		before := bar.TimeOf(effect.Buff)
		what, err := bar.Renew(effect.Buff, ticks)
		if err != nil {
			return nil, &Error{Message: err.Error()}
		}
		row := HeldBuff{Effect: effect.Key, Buff: effect.Buff, Name: effect.Name, What: what}
		if before > ticks {
			deferred = append(deferred, row)
		} else {
			held = append(held, row)
		}
	}
	return map[string]any{"held": held, "deferred": deferred}, nil
}
