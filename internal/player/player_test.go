package player_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/player"
)

/*
The two handles leave the same bytes behind.

Nothing here asserts a number this wrote. A write to a player is a write into a
running game, and being one field out is not an error message: it lands in
whatever the game keeps next door. So each case runs the same operations over
the same planted memory in both languages and compares the *whole buffer*,
which catches a field written in the wrong place as surely as a field written
with the wrong value.
*/

const (
	base = 0x10000000
	size = 0x1000
	life = base + 0x800
)

// preamble plants the same player in the Python's fake as the Go tests plant in
// theirs, and leaves a handle on it.
/*
plant is a Go fake holding the same player.

All six numbers differ from each other on purpose. A block of equal values reads
the same from the wrong offset as from the right one, which is exactly the
mistake these tests exist to catch.
*/
func plant(t *testing.T) (*memtest.FakeMem, *player.Player) {
	t.Helper()
	mem := memtest.New(base, size)
	mem.PlantPlayer(life, []int32{380, 400, 137, 201, 220, 180}, 0)
	return mem, player.New(mem, life)
}

/*
Every field is read from the place it was planted.

The block is six adjacent integers and the fields either side of the anchor are
read by subtracting from it, so a sign error reads the cap as the current value
-- which is a plausible number and looks like the player being at full health.
*/
func TestReadingAPlayer(t *testing.T) {
	_, p := plant(t)
	for name, c := range map[string]struct {
		read func() (int32, bool)
		want int32
	}{
		"life":      {p.StatLife, 137},
		"life_max":  {p.StatLifeMax, 400},
		"life_max2": {p.StatLifeMax2, 380},
		"mana":      {p.StatMana, 201},
		"mana_max":  {p.StatManaMax, 220},
		"mana_max2": {p.StatManaMax2, 180},
	} {
		got, ok := c.read()
		require.Truef(t, ok, "%s could not be read", name)
		require.Equalf(t, c.want, got, "%s is read from somewhere else", name)
	}
}

/*
Every write leaves the buffer in a known state, byte for byte.

The digest is over the whole buffer, because the fields are adjacent: a write
that put the right number one field along would otherwise pass, and the field
one along is the cap.
*/
func TestWritingAPlayer(t *testing.T) {
	for _, c := range []struct {
		name   string
		digest string
		run    func(p *player.Player) bool
	}{
		{"set_life", "da9d9c4c8c0ac138", func(p *player.Player) bool { return p.SetLife(1) }},
		{"set_mana", "27fd8c7af9ed2685", func(p *player.Player) bool { return p.SetMana(7) }},
		{"set_max_life", "1abc2dee5858dfdd", func(p *player.Player) bool { return p.SetMaxLife(500) }},
		{"set_max_mana", "b5ce83101d4a1d35", func(p *player.Player) bool { return p.SetMaxMana(400) }},
		{"heal_full", "09272104de09215d", func(p *player.Player) bool { return p.HealFull() }},
		{"mana_full", "911f2e6da92d4863", func(p *player.Player) bool { return p.ManaFull() }},
		// Raising the cap and then filling to it is the sequence the trainer
		// actually performs, and it is where writing only the permanent field
		// would leave the fill short.
		{"max then full", "480e816f4ca35a60", func(p *player.Player) bool {
			return p.SetMaxLife(500) && p.HealFull()
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			mem, p := plant(t)
			require.True(t, c.run(p), "the write was refused")
			require.Equal(t, c.digest, image(mem),
				"different bytes; if that was deliberate, update the digest")
		})
	}
}

// image is the whole planted buffer, as one short string.
func image(mem *memtest.FakeMem) string {
	sum := sha256.Sum256([]byte(mem.Hex()))
	return hex.EncodeToString(sum[:8])
}

/*
A player whose memory has gone is reported, not guessed at.

The object moves when the managed heap collects, and an address that stops
reading is how that shows. Filling to a cap that could not be read would write
whatever the last value happened to be.
*/
func TestAnUnreadablePlayerIsRefused(t *testing.T) {
	mem, _ := plant(t)
	gone := player.New(mem, 0x20000000)

	_, ok := gone.StatLife()
	require.False(t, ok, "life was read from unmapped memory")
	require.False(t, gone.HealFull(), "a player that could not be read was healed")
	require.False(t, gone.ManaFull(), "and had their mana filled")
	require.False(t, gone.SetLife(1), "and was written to")
}
