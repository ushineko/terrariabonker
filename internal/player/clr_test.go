package player_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/player"
)

/*
Under the CLR, the caps are read from their own fields: statLifeMax first,
statLifeMax2 second. Healing to full fills to the boosted cap. A handle using
mono's offsets would read the permanent cap where the boosted one is and heal a
buffed player short.
*/
func TestTheCLRHandleReadsAndHealsToTheBoostedCap(t *testing.T) {
	const base, life = 0x10000000, 0x10000800
	mem := memtest.New(base, 0x1000)
	mem.PlantCLRPlayer(life, []int32{400, 500, 300, 150, 200, 260}, base+0x40)
	e, _ := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	p := player.NewFor(e, mem, life)

	got, _ := p.StatLifeMax()
	require.EqualValues(t, 400, got, "the permanent cap")
	got, _ = p.StatLifeMax2()
	require.EqualValues(t, 500, got, "the boosted cap")

	require.True(t, p.HealFull())
	require.True(t, p.ManaFull())
	got, _ = p.StatLife()
	require.EqualValues(t, 500, got, "healed to the permanent cap, not the boosted one")
	got, _ = p.StatMana()
	require.EqualValues(t, 260, got)

	require.True(t, p.SetMaxLife(450))
	stored0, _ := mem.ReadI32(life - 8)
	stored1, _ := mem.ReadI32(life - 4)
	require.Equal(t, [2]int32{450, 450}, [2]int32{stored0, stored1}, "both caps")
}
