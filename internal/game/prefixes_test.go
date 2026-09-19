package game_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
The modifier table: what each is called, what it does, and which items can roll
it.

Two halves with different guarantees. The names and the multipliers are
extracted data in data/, so the file in the repository is the record and a
change to one is a visible change to it. The pools and the good/bad split are
not: they are Terraria's own categorisation, written down by hand -- and a
hand-written table is the thing that drifts.

So those two are frozen by digest. A change to either then fails here and has to
be confirmed, which is the whole point: the values themselves are in the source
a few lines away, and re-typing them into a test would be a second copy of
exactly the kind this guards.

The digests are of the table as it stood when it was last compared, entry for
entry, with the implementation this was ported from.
*/

const (
	qualityDigest = "3e3539c4b6461b706d0fbbbffe1ca1fdce4248f8ac362e6ccf2b04dcbd508231"
	poolDigest    = "5f2e81670425807fb36523b0f24882e1a0e58b906c4593e600b2f6080e49d57c"
)

// digest is a table's canonical form, for freezing one without copying it.
func digest(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

/*
Every modifier is judged good, bad, neutral or absent, and the judgement is
frozen.

This is the hand-written half. A modifier that changed from bad to good would
put a penalty at the top of the editor's list, which is a thing somebody would
apply to a weapon and then wonder about.
*/
func TestTheQualitySplitIsWhatItWas(t *testing.T) {
	px, err := game.ItemPrefixes()
	require.NoError(t, err)

	quality := map[string]string{}
	for id := range 120 {
		quality[itoa(id)] = px.Quality(id)
	}
	require.Equal(t, qualityDigest, digest(t, quality),
		"the good/bad split has changed; if that was deliberate, update the digest")
	require.Equal(t, "none", px.Quality(0), "no modifier is not a bad one")
}

/*
Every class combination rolls the same pool, in the same order.

Thirty-two combinations rather than the five flags alone, because an item can be
more than one damage class and the pools add up. The order is the dropdown's, so
a change that returned the same set differently ordered would put the modifier
somebody wanted somewhere else in the list.
*/
func TestTheModifierPoolsAreWhatTheyWere(t *testing.T) {
	px, err := game.ItemPrefixes()
	require.NoError(t, err)

	pools := map[string][]int{}
	for _, flags := range everyClassCombination() {
		pools[classKey(flags)] = orEmpty(px.Valid(flags))
	}
	require.Len(t, pools, 32, "a damage class has appeared or gone")
	require.Equal(t, poolDigest, digest(t, pools),
		"the modifier pools have changed; if that was deliberate, update the digest")

	// And an item of no class at all can take nothing, which is what stops the
	// editor offering modifiers for a stack of dirt.
	require.Empty(t, px.Valid(map[string]bool{}), "a classless item rolls modifiers")
	require.False(t, game.HasCategories(map[string]bool{}),
		"a classless item was reported as able to take one")
}

// everyClassCombination is the thirty-two ways an item's damage classes can be
// set.
func everyClassCombination() []map[string]bool {
	classes := append([]string{}, game.ClassFlags...)
	sort.Strings(classes)
	out := make([]map[string]bool, 0, 1<<len(classes))
	for bits := range 1 << len(classes) {
		flags := map[string]bool{}
		for i, class := range classes {
			if bits&(1<<i) != 0 {
				flags[class] = true
			}
		}
		out = append(out, flags)
	}
	return out
}

// classKey names one combination, for the frozen table's keys.
func classKey(flags map[string]bool) string {
	var on []string
	for class, set := range flags {
		if set {
			on = append(on, class)
		}
	}
	sort.Strings(on)
	raw, _ := json.Marshal(on)
	return string(raw)
}

/*
The names and the multipliers come out of the bundled data, and are spot-checked
rather than frozen.

The file in the repository is the record for those: a change to it is a change
to a committed file, which is visible without a test saying so. What is worth
asserting is that it is being read at all, and read into the right shape.
*/
func TestTheBundledModifierDataIsRead(t *testing.T) {
	px, err := game.ItemPrefixes()
	require.NoError(t, err)

	require.NotEmpty(t, px.All(), "no modifiers were loaded")
	require.Equal(t, "Legendary", px.Name(81), "modifier 81 is not the one it was")
	require.Equal(t, "Large", px.Name(1))
	require.Empty(t, px.Name(0), "no modifier has no name")

	legendary := px.Stats(81)
	require.NotEmpty(t, legendary, "the best melee modifier does nothing")
	require.Contains(t, legendary, "damage", "it does not touch the damage")

	// The three fields that are added rather than multiplied.
	require.Len(t, game.AdditiveStats, 3)
	for _, field := range []string{"crit", "tagdamage", "armorpen"} {
		require.Truef(t, game.AdditiveStats[field], "%q is not added", field)
	}
}

// orEmpty is a pool as the frozen table spells it: an empty list rather than
// nothing.
func orEmpty(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}
