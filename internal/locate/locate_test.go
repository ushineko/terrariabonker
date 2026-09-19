package locate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
What counts as a player, and where they are.

Being wrong here is not an error message. The addresses this returns are written
to, so a rule that is too loose finds something that is not a player and the
trainer edits it; a rule that is too tight misses the live copy and every write
lands on an inert snapshot the game ignores -- which has happened, and is why
the boost headroom exists.

Every expectation here was agreed with the implementation this was ported from,
while both existed.
*/

// blocks are the cases a life and mana block is judged on: the ordinary ones,
// and every edge the rule has a reason for.
var blocks = []struct {
	block []int32
	why   string
	valid bool
}{
	{[]int32{400, 400, 400, 200, 200, 200}, "a plain, full player", true},
	{[]int32{420, 400, 420, 220, 200, 220}, "boosted caps, current at the boosted one", true},
	{[]int32{400, 400, 1, 0, 200, 200}, "one hit point and no mana, which is legal", true},
	{[]int32{400, 400, 401, 200, 200, 200}, "life above the boosted cap", false},
	{[]int32{400, 400, 0, 200, 200, 200}, "no life at all: not a living player", false},
	{[]int32{400, 402, 400, 200, 200, 200}, "a cap that is not a multiple of five", false},
	{[]int32{400, 95, 95, 200, 200, 200}, "below the smallest real cap", false},
	{[]int32{400, 505, 505, 200, 200, 200}, "above the largest", false},
	{[]int32{901, 400, 400, 200, 200, 200}, "boosted life beyond the headroom", false},
	{[]int32{400, 400, 400, 200, 210, 210}, "a mana cap that is not a multiple of twenty", false},
	{[]int32{400, 400, 400, 200, 420, 420}, "a mana cap beyond the largest", false},
	{[]int32{400, 400, 400, 221, 200, 220}, "mana above the boosted cap", false},
	{[]int32{400, 400, 400, -1, 200, 200}, "negative mana", false},
	{[]int32{0, 0, 0, 0, 0, 0}, "empty memory", false},
	{[]int32{-1, -1, -1, -1, -1, -1}, "and memory that is not a block at all", false},
}

// Every rule about what a player's block looks like.
func TestValidBlock(t *testing.T) {
	for _, c := range blocks {
		require.Equalf(t, c.valid, locate.ValidBlock(c.block), "%s: %v", c.why, c.block)
	}
	require.False(t, locate.ValidBlock([]int32{1, 2, 3}), "too few numbers is not a block")
}

/*
A name is only a name if it reads like one.

Any four bytes of heap can be read as a pointer; very little of what they point
at is a printable ASCII string of a plausible length. That is what turns a
coincidence into a match -- and why a name with an accent in it is refused,
which is a real name the locator will not recognise and a trade this project
made deliberately.
*/
func TestReadMonoString(t *testing.T) {
	const base, size = 0x10000000, 0x400
	for _, c := range []struct{ planted, want string }{
		{"Nakama", "Nakama"},
		{"a", "a"},
		{strings.Repeat("x", 64), strings.Repeat("x", 64)},
		{"has space", "has space"},
		{"Zoë", ""}, // not ASCII, so not a name this will accept
	} {
		mem := memtest.New(base, size)
		mem.PlantMonoString(base+0x40, c.planted)

		got, ok := locate.ReadMonoString(mem, base+0x40)
		if c.want == "" {
			require.Falsef(t, ok, "%q was read as a name", c.planted)
			continue
		}
		require.Truef(t, ok, "%q was not read as a name", c.planted)
		require.Equal(t, c.want, got)
	}

	// A length nothing could be, and a pointer into nothing.
	mem := memtest.New(base, size)
	mem.PokeI32(base+0x40+8, 999)
	_, ok := locate.ReadMonoString(mem, base+0x40)
	require.False(t, ok, "a length no name has")
	_, ok = locate.ReadMonoString(mem, base+uint32(size))
	require.False(t, ok, "and a pointer past the end of the region")
}

/*
A scan finds the players and nothing else.

A player is planted twice -- the live copy and a snapshot, which is what a real
game looks like -- plus a block that passes the cheap prefilter and has no name
behind it, which is what random memory looks like.
*/
func TestFindPlayers(t *testing.T) {
	const base, size = 0x10000000, 0x4000

	mem := memtest.New(base, size)
	mem.PlantMonoString(base+0x40, "Nakama")
	mem.PlantPlayer(base+0x800, []int32{420, 400, 420, 220, 200, 220}, base+0x40)
	mem.PlantPlayer(base+0x1800, []int32{400, 400, 400, 200, 200, 200}, base+0x40)
	// A near miss: it passes the prefilter and has nothing readable where a
	// name pointer would be.
	mem.PokeI32(base+0x2000, 400)
	mem.PokeI32(base+0x2004, 400)
	mem.PokeI32(base+0x2008, 400)

	got := locate.FindPlayers(mem)
	require.Len(t, got, 1, "a different number of players was found")
	require.EqualValues(t, base+0x1800, got[0].LifeAddr, "the player is somewhere else")
	require.Equal(t, "Nakama", got[0].Name)
	require.Equal(t, []int32{400, 400, 400, 200, 200, 200}, got[0].Fields())
}

/*
Reading a block at a known address, including where there is no player.

A cached address being re-checked has to report that it has gone: the managed
heap is collected, so an address that held a player stops being one.
*/
func TestReadBlock(t *testing.T) {
	const base, size = 0x10000000, 0x4000
	mem := memtest.New(base, size)
	mem.PlantMonoString(base+0x40, "Nakama")
	mem.PlantPlayer(base+0x800, []int32{400, 400, 400, 200, 200, 200}, base+0x40)

	for _, c := range []struct {
		addr  uint32
		there bool
	}{
		{base + 0x800, true},
		{base + 0x900, false},
		{base + 0x3FFF, false},
	} {
		block, ok := locate.ReadBlock(mem, c.addr)
		require.Equalf(t, c.there, ok, "%#x", c.addr)
		if ok {
			require.Equal(t, "Nakama", block.Name)
			require.Equal(t, c.addr, block.LifeAddr)
		}
	}
}

/*
The guess at which copy is live.

It is only reached when the resolver cannot answer, and it is wrong often enough
that the comments say so -- what matters is that it refuses rather than guesses
when there is nothing to tell the copies apart, because a caller that picked
wrong would start editing a corpse.

Nothing here moves while it is sampled: a planted buffer is as frozen as a
paused game, which is the case the fallbacks exist for and the one that actually
happens.
*/
func TestPickingTheLiveCopy(t *testing.T) {
	for _, c := range []struct {
		name    string
		planted [][2]any // address, block
		want    uint32
	}{
		{
			name:    "one copy",
			planted: [][2]any{{uint32(0x10000800), []int32{400, 400, 400, 200, 200, 200}}},
			want:    0x10000800,
		},
		{
			// Two frozen copies, one of them hurt: the only one below its cap wins.
			name: "one below its cap",
			planted: [][2]any{
				{uint32(0x10000800), []int32{400, 400, 400, 200, 200, 200}},
				{uint32(0x10001800), []int32{400, 400, 137, 200, 200, 200}},
			},
			want: 0x10001800,
		},
		{
			// Both hurt: there is nothing to tell them apart, so neither is picked.
			name: "both below",
			planted: [][2]any{
				{uint32(0x10000800), []int32{400, 400, 300, 200, 200, 200}},
				{uint32(0x10001800), []int32{400, 400, 137, 200, 200, 200}},
			},
		},
		{
			// Both at full life, which is the paused idle player: give up.
			name: "neither below",
			planted: [][2]any{
				{uint32(0x10000800), []int32{400, 400, 400, 200, 200, 200}},
				{uint32(0x10001800), []int32{400, 400, 400, 200, 200, 200}},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mem := memtest.New(0x10000000, 0x4000)
			mem.PlantMonoString(0x10000040, "Nakama")
			for _, p := range c.planted {
				mem.PlantPlayer(p[0].(uint32), p[1].([]int32), 0x10000040)
			}

			got, ok := locate.PickLive(mem, locate.FindPlayers(mem), 2, 0)
			if c.want == 0 {
				require.False(t, ok, "a copy was picked with nothing to tell them apart")
				return
			}
			require.True(t, ok, "no copy was picked")
			require.Equal(t, c.want, got.LifeAddr, "a different copy")
		})
	}
}
