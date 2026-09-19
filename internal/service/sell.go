package service

import (
	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/selling"
)

/*
Auto-selling: take a whitelisted stack and pay for it in coins.

The order is the whole safety property. **Pay first, then take** -- the other way
round loses the item if the credit fails.
*/

// Reach is whether the player can get at their bank, and how.
type Reach struct {
	Reachable bool    `json:"reachable"`
	Why       string  `json:"why"`
	Carried   []int32 `json:"carried"`
	Placed    *bool   `json:"placed"`
}

/*
BankReachable says whether the player can open their Piggy Bank *in this world*
right now.

Two ways, and either is enough: they carry something that deploys one anywhere,
or one is already placed in the loaded world.

The distinction matters because the bank is character state, not world state.
Coins put there while the player has no way to open a piggy bank are not lost,
but they are unreachable until the player finds one -- which is the outcome this
exists to avoid.
*/
func (s *Service) BankReachable(rescan bool) (Reach, error) {
	inv, err := s.liveInventory()
	if err != nil {
		return Reach{}, err
	}
	carried := []int32{}
	for _, want := range []int32{selling.PiggyBankItem, selling.MoneyTroughItem} {
		for _, slot := range inv.Slots() {
			if !slot.Empty() && slot.Type == want {
				carried = append(carried, want)
				break
			}
		}
	}
	if len(carried) > 0 {
		return Reach{Reachable: true, Why: "carried", Carried: carried}, nil
	}

	/*
		A whole-world tile search costs a fraction of a second, which is nothing
		once and far too much on a timer. So the answer is kept per world and
		asked again only when the world has changed.
	*/
	world, _ := s.WorldID()
	if rescan || s.bankPlaced == nil || s.bankWorld != world {
		tm, err := s.TileMap()
		if err != nil {
			return Reach{}, err
		}
		found := len(tm.FindType(selling.PiggyBankTile, 1)) > 0
		s.bankPlaced, s.bankWorld = &found, world
	}
	placed := *s.bankPlaced
	why := "no bank in reach"
	if placed {
		why = "placed"
	}
	return Reach{Reachable: placed, Why: why, Carried: []int32{}, Placed: &placed}, nil
}

/*
creditCoins puts an amount into a container as coins, and is what it placed and
what it could not.

Every write re-resolves its slot and checks what is in it. Nothing here writes
through an address read earlier: a round reads forty slots before it writes to
any of them, and the heap moves objects in between -- opening a chest is exactly
that kind of allocation.

Merging into an existing stack comes first, and a stack is capped, which is what
the game enforces. Whatever there is no room for is handed back rather than
quietly dropped, so the caller can pay it elsewhere or refuse the sale.
*/
func (s *Service) creditCoins(c *selling.Container, copper int32) ([]selling.Coins, int32) {
	placed := []selling.Coins{}
	pending := selling.CoinStacks(copper)

	for i, want := range pending {
		count := want.Stack
		for count > 0 {
			rows := c.Rows()
			var spot *selling.Row
			for j := range rows {
				if rows[j].Type == want.Type && rows[j].Stack < selling.CoinMaxStack {
					spot = &rows[j]
					break
				}
			}
			var n int32
			var ok bool
			if spot != nil {
				n = min32(selling.CoinMaxStack-spot.Stack, count)
				total := spot.Stack + n
				ok = c.Write(spot.Index, selling.Change{
					ExpectType: want.Type, ExpectStack: spot.Stack, Stack: &total,
				})
			} else {
				var empty *selling.Row
				for j := range rows {
					if rows[j].Type == 0 {
						empty = &rows[j]
						break
					}
				}
				if empty == nil {
					return placed, owed(pending[i:], count)
				}
				n = min32(selling.CoinMaxStack, count)
				block, _ := s.TemplateBlock(want.Type)
				coin := want.Type
				ok = c.Write(empty.Index, selling.Change{
					ExpectType: 0, ExpectStack: 0,
					ItemType: &coin, Stack: &n, Block: block,
				})
			}
			if !ok {
				// The slot changed underneath. Stop and hand back what is unpaid
				// rather than writing into whatever is there now.
				return placed, owed(pending[i:], count)
			}
			placed = append(placed, selling.Coins{Type: want.Type, Stack: n})
			count -= n
		}
	}
	s.normalizeCoins(c)
	return placed, 0
}

// owed is what a part-finished credit still has to pay, in copper.
func owed(left []selling.Coins, firstCount int32) int32 {
	var total int32
	for i, c := range left {
		count := c.Stack
		if i == 0 {
			count = firstCount
		}
		for j, t := range selling.CoinTypes {
			if t == c.Type {
				total += count * selling.CoinWorth[j]
			}
		}
	}
	return total
}

/*
normalizeCoins promotes any full coin stack the way the game does: a hundred of a
denomination becomes one of the next up, merged into an existing stack where
there is one.

Without this the credit is *worth* the right amount but leaves a state the game
would never leave -- a stack of exactly a hundred silver where one gold belongs.
Observed in the game: a sale merged seventeen onto a stack of eighty-three and
stopped at a hundred.

The top denomination has nothing to promote into, so it is left alone; the game
stacks it past a hundred too.
*/
func (s *Service) normalizeCoins(c *selling.Container) {
	for _, ctype := range selling.CoinTypes[:len(selling.CoinTypes)-1] {
		next := ctype + 1
		for {
			rows := c.Rows()
			var full, into, spare *selling.Row
			for j := range rows {
				switch {
				case full == nil && rows[j].Type == ctype && rows[j].Stack >= selling.CoinMaxStack:
					full = &rows[j]
				case into == nil && rows[j].Type == next && rows[j].Stack < selling.CoinMaxStack:
					into = &rows[j]
				case spare == nil && rows[j].Type == 0:
					spare = &rows[j]
				}
			}
			if full == nil {
				break
			}
			var ok bool
			if into != nil {
				total := into.Stack + 1
				ok = c.Write(into.Index, selling.Change{
					ExpectType: next, ExpectStack: into.Stack, Stack: &total,
				})
			} else {
				if spare == nil {
					return // nowhere to put it: leave it be
				}
				block, _ := s.TemplateBlock(next)
				one, coin := int32(1), next
				ok = c.Write(spare.Index, selling.Change{
					ExpectType: 0, ExpectStack: 0,
					ItemType: &coin, Stack: &one, Block: block,
				})
			}
			if !ok {
				return // the slot moved: stop, do not guess
			}
			left := full.Stack - selling.CoinMaxStack
			change := selling.Change{
				ExpectType: ctype, ExpectStack: full.Stack, Stack: &left,
			}
			if left == 0 {
				empty := int32(0)
				change.ItemType = &empty
			}
			c.Write(full.Index, change)
		}
	}
}

// Sold is one stack that was taken.
type Sold struct {
	Slot   int    `json:"slot"`
	Type   int32  `json:"type"`
	Name   string `json:"name"`
	Stack  int32  `json:"stack"`
	Copper int32  `json:"copper"`
}

// Skipped is one stack that was left.
type Skipped struct {
	Slot int    `json:"slot"`
	Type int32  `json:"type"`
	Why  string `json:"why"`
}

/*
SellTick sells every whitelisted, unfavorited stack once. One round.

Stateless like the other rounds, so a fresh process and a timer behave
identically.

**Favorited stacks are never sold**, whitelist or not. That is what the game does
when selling, and it is the player's only per-stack override on a per-type
whitelist -- on a cheat whose effect is permanent once the world saves, that makes
it a correctness requirement rather than a convenience.
*/
func (s *Service) SellTick(dryRun bool) (map[string]any, error) {
	wanted := map[int32]bool{}
	for _, t := range profile.SellWhitelist() {
		wanted[t] = true
	}
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	live, err := s.LiveBlock()
	if err != nil {
		return nil, err
	}
	names, err := game.ItemNames()
	if err != nil {
		return nil, err
	}
	reach, err := s.BankReachable(false)
	if err != nil {
		return nil, err
	}

	array := func() (uint32, bool) { return inv.ArrayAddr() }
	purse := &selling.Container{Mem: s.Mem, Array: array, Slots: layout.SellSlots}
	// Coins go only where the game's own sell path puts them; the scan may look
	// further.
	coinPurse := &selling.Container{Mem: s.Mem, Array: array, Slots: layout.CoinSlots}

	var bank *selling.Container
	dest := "inventory"
	if reach.Reachable {
		if got, ok := selling.Bank(s.Mem, live.LifeAddr); ok {
			bank, dest = got, "bank"
		}
	}

	sold, skipped := []Sold{}, []Skipped{}
	var total int32
	for _, row := range purse.Rows() {
		if row.Type == 0 || !wanted[row.Type] {
			continue
		}
		if row.Favorited {
			skipped = append(skipped, Skipped{Slot: row.Index, Type: row.Type, Why: "favorited"})
			continue
		}
		/*
			A whitelisted item worth nothing is still taken. The list is the
			player's statement of intent, and for junk drops "sell it" and "bin
			it" are the same request -- refusing would leave the one thing they
			most want gone sitting there. It pays nothing, which is honest.
		*/
		price := selling.Price(row.Value, row.Stack)
		sold = append(sold, Sold{
			Slot: row.Index, Type: row.Type, Name: names.Label(int(row.Type)),
			Stack: row.Stack, Copper: price,
		})
		total += price
	}

	out := map[string]any{
		"sold": sold, "skipped": skipped, "copper": total,
		"destination": dest, "reachable": reach, "dry_run": dryRun,
		"paid": []selling.Coins{},
	}
	if dryRun || len(sold) == 0 {
		out["unpaid"] = int32(0)
		if !dryRun {
			out["unpaid"] = total
		}
		return out, nil
	}

	// Pay first, then take. The other order loses the item if the credit fails.
	into := coinPurse
	if bank != nil {
		into = bank
	}
	paid, unpaid := s.creditCoins(into, total)
	if unpaid > 0 && bank != nil {
		more, still := s.creditCoins(coinPurse, unpaid)
		paid, unpaid = append(paid, more...), still
		dest = "bank+inventory"
	}
	if unpaid > 0 {
		return map[string]any{
			"sold": []Sold{}, "skipped": skipped, "copper": int32(0),
			"destination": dest, "reachable": reach, "dry_run": false,
			"paid": paid, "unpaid": unpaid,
			"error": "no room for the coins -- nothing was sold",
		}, nil
	}

	invs, err := s.allInventories()
	if err != nil {
		return nil, err
	}
	for _, entry := range sold {
		for _, i := range invs {
			i.SetType(entry.Slot, 0)
			i.SetStack(entry.Slot, 0)
		}
	}
	out["destination"], out["paid"], out["unpaid"] = dest, paid, int32(0)
	return out, nil
}

// min32 is the smaller of two words.
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
