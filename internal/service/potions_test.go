package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
)

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
