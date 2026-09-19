package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/selling"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Auto-selling, whose effect is permanent the moment the world saves.

Two rules carry that weight. The player is **paid before the item is taken** --
the other order loses it if the credit fails. And a favorited stack is never
sold, whitelist or not: it is the player's only per-stack override on a per-type
list, which makes it a correctness requirement rather than a convenience.
*/

// A round with nothing whitelisted takes nothing.
func TestSellingNothingWhitelisted(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBankInto(mem)
	before := mem.Hex()

	got, err := service.New(mem, -1).SellTick(false)
	require.NoError(t, err)
	require.Empty(t, got["sold"], "something was sold with nothing on the list")
	require.Equal(t, before, mem.Hex(), "something was written")
}

/*
A dry run says what it would do and writes nothing.

Nothing about selling is undoable, so the part that decides what goes is worth
being able to inspect on its own.
*/
func TestADryRunWritesNothing(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	plantSellableInto(mem)
	before := mem.Hex()

	got, err := service.New(mem, -1).SellTick(true)
	require.NoError(t, err)
	require.NotEmpty(t, got["sold"], "a dry run found nothing to sell")
	require.True(t, got["dry_run"].(bool))
	require.Equal(t, before, mem.Hex(), "a dry run wrote something")
}

/*
A favorited stack is never sold.

It is the player's only per-stack override on a per-type whitelist, and the
effect is permanent once the world saves.
*/
func TestAFavoritedStackIsNeverSold(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	plantSellableInto(mem)
	plantFavoriteOnly(mem, sellableSlot)

	got, err := service.New(mem, -1).SellTick(false)
	require.NoError(t, err)
	require.Empty(t, got["sold"], "a favorited stack was sold")

	skipped := got["skipped"].([]service.Skipped)
	require.Len(t, skipped, 1, "the favorited stack was not reported as skipped")
	require.Equal(t, "favorited", skipped[0].Why)
}

/*
The player is paid, and only then is the item taken.

A round that took first and failed to pay would lose the stack outright.
*/
func TestSellingPaysBeforeTaking(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	plantSellableInto(mem)
	svc := service.New(mem, -1)

	got, err := svc.SellTick(false)
	require.NoError(t, err)
	require.NotEmpty(t, got["sold"], "nothing was sold")
	require.Zero(t, got["unpaid"], "the sale left something unpaid")
	require.NotEmpty(t, got["paid"], "nothing was paid")

	// The stack is gone, and coins are where the payment went.
	inv, err := svc.Inventory()
	require.NoError(t, err)
	for _, slot := range inv {
		if slot.Slot == sellableSlot {
			require.Equal(t, int32(0), slot.Type, "the stack was not taken")
		}
	}
}

/*
With no room for the coins, nothing is sold at all.

Paying first is only a safety property if failing to pay stops the sale.
*/
func TestNoRoomForTheCoinsSellsNothing(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	fillEverySlot(mem)
	plantSellableInto(mem)
	// A bank they carry but cannot open here, so the coins have to go into the
	// inventory -- where there is no room for them.
	plantCarriedBank(mem)

	got, err := service.New(mem, -1).SellTick(false)
	require.NoError(t, err)
	require.Empty(t, got["sold"], "a stack was taken with nowhere to put the coins")
	require.NotZero(t, got["unpaid"], "nothing was reported unpaid")
	require.Contains(t, got, "error")
}

/*
A bank the player carries is reachable; one they do not is not.

The bank is character state rather than world state, so coins put there while
they have no way to open one are unreachable until they find one.
*/
func TestBankReachableMatchesThePython(t *testing.T) {
	atHome(t)
	mem := plant()
	plantWorldInto(mem, "Nakama's World")

	got, err := service.New(mem, -1).BankReachable(false)
	require.NoError(t, err)
	require.False(t, got.Reachable, "a bank was reachable with none carried or placed")
	require.Equal(t, "no bank in reach", got.Why)

	// They pick one up.
	plantCarriedBank(mem)
	got, err = service.New(mem, -1).BankReachable(false)
	require.NoError(t, err)
	require.True(t, got.Reachable, "a carried bank was not reachable")
	require.Equal(t, "carried", got.Why)
	require.Equal(t, []int32{selling.PiggyBankItem}, got.Carried)
}

/*
Whether a bank is placed is asked of the world once, not on every round.

A whole-world tile search is nothing once and far too much on a timer, so the
answer is kept per world. What that means is observable without timing anything:
a bank placed after the question was first asked is not noticed until somebody
says to look again.
*/
func TestThePlacedBankIsAskedOnce(t *testing.T) {
	atHome(t)
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	svc := service.New(mem, -1)

	got, err := svc.BankReachable(false)
	require.NoError(t, err)
	require.False(t, got.Reachable, "a bank was found in a world with none in it")

	// One is placed. The kept answer stands until somebody asks for a rescan.
	plantTile(mem, 20, 20, selling.PiggyBankTile, true)

	got, err = svc.BankReachable(false)
	require.NoError(t, err)
	require.False(t, got.Reachable, "the world was searched again on an ordinary round")

	got, err = svc.BankReachable(true)
	require.NoError(t, err)
	require.True(t, got.Reachable, "a rescan did not find the bank that was placed")
	require.Equal(t, "placed", got.Why)
}

/*
A different world is a different answer.

The bank is world state here, so carrying the last world's answer into a new one
would tell somebody they can reach a bank that is not there.
*/
func TestADifferentWorldIsAskedAgain(t *testing.T) {
	atHome(t)
	mem := plant()
	plantWorldInto(mem, "Nakama's World")
	plantTile(mem, 20, 20, selling.PiggyBankTile, true)
	svc := service.New(mem, -1)

	got, err := svc.BankReachable(false)
	require.NoError(t, err)
	require.True(t, got.Reachable, "the bank that was planted was not found")

	// They travel. The new world has no bank in it.
	plantWorldInto(mem, "Somewhere Else")
	plantTile(mem, 20, 20, 1, true)

	got, err = svc.BankReachable(false)
	require.NoError(t, err)
	require.False(t, got.Reachable, "the last world's answer was carried into a new one")
}

/*
Coins merge into a stack that is already there, and a full stack is promoted.

Observed in the game: a sale merged seventeen silver onto a stack of eighty-three
and stopped at a hundred. The credit was worth the right amount but left a state
the game would never leave -- a hundred silver where one gold belongs.
*/
func TestCoinsMergeAndPromote(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	// Eighty-three silver already in the bank, and something worth seventeen
	// more.
	plantBankCoins(mem, 0, selling.CoinTypes[1], 83)
	plantSellableWorth(mem, 8500, 1)

	got, err := service.New(mem, -1).SellTick(false)
	require.NoError(t, err)
	require.NotEmpty(t, got["sold"], "nothing was sold")
	require.Zero(t, got["unpaid"], "the sale left something unpaid")

	stacks := bankStacks(mem)
	for _, s := range stacks {
		if s.Type == selling.CoinTypes[len(selling.CoinTypes)-1] {
			continue // the top denomination stacks past a hundred, as the game does
		}
		require.LessOrEqualf(t, s.Stack, int32(selling.CoinMaxStack),
			"a stack of %d of coin %d was left, which the game would never leave",
			s.Stack, s.Type)
	}

	// And the hundred silver became a gold.
	var gold int32
	for _, s := range stacks {
		if s.Type == selling.CoinTypes[2] {
			gold += s.Stack
		}
	}
	require.Equal(t, int32(1), gold, "a full silver stack was not promoted to gold")
}

/*
A coin stack is never filled past what the game allows, even when there is
nowhere to promote it.

The promotion normally hides an over-filled stack: a hundred and twenty silver
becomes a gold and twenty, which is the same state a correct merge reaches by
another route. It only shows when there is no room for the gold -- and then the
choice is between refusing the sale and leaving a stack the game would never
leave.
*/
func TestACoinStackIsCappedEvenWithNowhereToPromote(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	mem := plant()
	plantBankInto(mem)
	fillEverySlot(mem)    // no room in the inventory either
	plantCarriedBank(mem) // and the bank they carry, which the fill wrote over

	// Ninety silver, and every other bank slot taken by something that is not a
	// coin: there is room to merge ten and nowhere at all for the rest.
	plantBankCoins(mem, 0, selling.CoinTypes[1], 90)
	for i := 1; i < layout.BankSlots; i++ {
		plantBankCoins(mem, i, 3507, 1)
	}
	plantSellableWorth(mem, 15000, 1) // three thousand copper, or thirty silver

	got, err := service.New(mem, -1).SellTick(false)
	require.NoError(t, err)
	require.Empty(t, got["sold"], "a sale went through with nowhere to put the coins")
	require.NotZero(t, got["unpaid"], "nothing was reported unpaid")

	for _, s := range bankStacks(mem) {
		require.LessOrEqualf(t, s.Stack, int32(selling.CoinMaxStack),
			"a stack of %d of coin %d was left behind", s.Stack, s.Type)
	}
}
