package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Passive potions: every favorited potion the player carries has its buff renewed.

Stateless on purpose, unlike the vein watcher: there is nothing to remember, and
re-reading the inventory each round is what makes favoriting a potion take effect
immediately rather than at the next rescan.
*/
func TestPotionTickMatchesThePython(t *testing.T) {
	for _, minStack := range []int32{1, 2, 10} {
		t.Run(itoa(int(minStack)), func(t *testing.T) {
			atHome(t)
			var want map[string]any
			askPython(t, preamble()+plantBuffs()+sprintf(`
print(json.dumps(svc.potion_tick(min_stack=%d)))`, minStack), &want)

			mem := plant()
			plantBuffsInto(mem)
			got, err := service.New(mem, -1).PotionTick(minStack, 0)
			require.NoError(t, err)

			for _, what := range []string{"added", "renewed", "kept", "full"} {
				require.Equalf(t, want[what], asJSON(t, got[what]),
					"a different set was %s", what)
			}
			require.Equal(t, want["carried"], asJSON(t, got["carried"]), "a different count")
			require.Equal(t, want["ticks"], asJSON(t, got["ticks"]), "a different renewal")
		})
	}
}

/*
Favoriting a potion takes effect on the next round, with no rescan.

The inventory is re-read every round, so a potion favorited a moment ago is
carried immediately -- which is what makes the cheat feel like it is watching
rather than sampling.
*/
func TestFavoritingAPotionTakesEffectImmediately(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)
	svc := service.New(mem, -1)

	first, err := svc.PotionTick(1, 0)
	require.NoError(t, err)
	before := first["carried"].(int)

	// The player favorites the potion in slot 2, which was not favorited.
	plantFavorite(mem, 2)

	after, err := svc.PotionTick(1, 0)
	require.NoError(t, err)
	require.Greater(t, after["carried"].(int), before,
		"a potion favorited between rounds was not picked up")
}
