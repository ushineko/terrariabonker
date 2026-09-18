package game_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
Every modifier's name and quality is the Python's answer.

The names are extracted data and could only differ by a decoding mistake. The
quality is not: it is a hand-written list of which modifiers are detrimental and
which do nothing, and it now exists in two languages. A constant spelled twice
is this project's oldest failure mode, so every id is asked rather than a sample.
*/
func TestEveryModifierIsNamedAndJudgedLikeThePython(t *testing.T) {
	var want map[string][]string // id -> [name, quality]
	askPython(t, `
import json
from terrariabonker import prefixes as px
ids = set(px.all_ids()) | set(range(0, 120))
print(json.dumps({str(i): [px.name(i), px.quality(i)] for i in sorted(ids)}))
`, &want)

	got, err := game.ItemPrefixes()
	require.NoError(t, err)
	require.NotEmpty(t, want)

	for key, pair := range want {
		id := atoi(t, key)
		require.Equalf(t, pair[0], got.Name(id), "modifier %d is named differently", id)
		require.Equalf(t, pair[1], got.Quality(id), "modifier %d is judged differently", id)
	}
	require.Equal(t, "none", got.Quality(0), "no modifier is not a bad one")
}

/*
Every class combination rolls the same pool, in the same order.

Thirty-two combinations rather than the five flags on their own, because an item
can be more than one damage class and the pools add up. The order is the
dropdown's, so a port that returned the same set differently ordered would put
the modifier someone wanted somewhere else in the list.
*/
func TestEveryClassCombinationRollsThePythonsPool(t *testing.T) {
	var want map[string][]int
	askPython(t, `
import itertools, json
from terrariabonker import prefixes as px
out = {}
for bits in itertools.product([False, True], repeat=len(px.CLASS_FLAGS)):
    flags = dict(zip(px.CLASS_FLAGS, bits))
    key = ",".join(c for c, on in flags.items() if on)
    out[key] = px.valid_prefixes(flags)
print(json.dumps(out))
`, &want)

	got, err := game.ItemPrefixes()
	require.NoError(t, err)
	require.Len(t, want, 32)

	for key, ids := range want {
		flags := map[string]bool{}
		for _, class := range splitClasses(key) {
			flags[class] = true
		}
		require.Equalf(t, ids, orEmpty(got.Valid(flags)), "%q rolls a different pool", key)
		require.Equalf(t, len(ids) > 0, game.HasCategories(flags),
			"%q disagrees about whether it can take a modifier at all", key)
	}
}

// Every modifier's effect on the item, which is what the editor writes: a
// difference here is a sword with the wrong damage.
func TestEveryModifiersEffectMatchesThePython(t *testing.T) {
	var want map[string]map[string]float64
	askPython(t, `
import json
from terrariabonker import prefixes as px
print(json.dumps({str(i): px.stat_multipliers(i) for i in range(0, 120)}))
`, &want)

	got, err := game.ItemPrefixes()
	require.NoError(t, err)
	for key, fields := range want {
		id := atoi(t, key)
		require.Equalf(t, fields, orEmptyStats(got.Stats(id)),
			"modifier %d does something different to the item", id)
	}

	// The fields that are added rather than multiplied are the same three.
	var additive []string
	askPython(t, `
import json
from terrariabonker import prefixes as px
print(json.dumps(sorted(px.ADDITIVE_STATS)))
`, &additive)
	require.Len(t, game.AdditiveStats, len(additive))
	for _, field := range additive {
		require.Truef(t, game.AdditiveStats[field], "%q is added on one side and not the other", field)
	}
}

// The full list, by name, which is what a picker shows for an item that has not
// been chosen yet.
func TestTheWholeModifierListIsOrderedLikeThePythons(t *testing.T) {
	var want []int
	askPython(t, `
import json
from terrariabonker import prefixes as px
print(json.dumps(px.all_ids()))
`, &want)

	got, err := game.ItemPrefixes()
	require.NoError(t, err)
	require.Equal(t, want, got.All())
}

// splitClasses reads the test key back into class names.
func splitClasses(key string) []string {
	if key == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(key); i++ {
		if i == len(key) || key[i] == ',' {
			out = append(out, key[start:i])
			start = i + 1
		}
	}
	return out
}

// orEmpty is a nil slice as the empty list the Python prints.
func orEmpty(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}

// orEmptyStats is the same for a modifier that does nothing to the item.
func orEmptyStats(fields map[string]float64) map[string]float64 {
	if fields == nil {
		return map[string]float64{}
	}
	return fields
}
