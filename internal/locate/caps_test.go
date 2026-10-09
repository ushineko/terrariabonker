package locate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
A buffed player is found under mono, with the caps the right way round.

Planted in the storage order the mono runtime reports by name (cmd/monofields
against the live game, 2026-10-08): statLifeMax, the permanent cap, at
statLife-8 (0x730), and statLifeMax2, the cap in effect, at statLife-4 (0x734).
Until then this project had the two names swapped, and ValidBlock took the
permanent cap for the boosted one -- so a player whose cap in effect was above the
permanent one failed validation and was not found at all. The second player's
cap in effect is past 500, which the prefilter, reading the wrong word as the
permanent cap, also dropped.
*/
func TestABuffedMonoPlayerIsFound(t *testing.T) {
	const base, size = 0x10000000, 0x8000
	mem := memtest.New(base, size)
	mem.PlantMonoString(base+0x40, "lifeforce")
	mem.PlantPlayer(base+0x1000, []int32{400, 480, 470, 200, 200, 220}, base+0x40)
	mem.PlantPlayer(base+0x4000, []int32{500, 600, 590, 200, 200, 200}, base+0x40)

	got := locate.FindPlayers(mem)
	require.Len(t, got, 2, "a buffed player was not found")
	require.EqualValues(t, 400, got[0].StatLifeMax, "statLifeMax is the permanent cap")
	require.EqualValues(t, 480, got[0].StatLifeMax2, "statLifeMax2 is the cap in effect")
	require.EqualValues(t, 600, got[1].StatLifeMax2)

	blk, ok := locate.ReadBlock(mem, base+0x1000)
	require.True(t, ok)
	require.Equal(t, got[0], blk)
}
