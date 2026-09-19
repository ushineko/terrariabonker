package buffs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/buffs"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
Holding an effect up, and the one rule that keeps a potion the player drank.

A renewal never takes time away. Somebody who drank an eight-minute potion has
eight minutes of it; renewing that to two seconds would leave them with two
seconds the moment they dropped the stack, and they would blame the potion rather
than the trainer. A slot already running longer is left completely alone -- not
rewritten with the larger of the two values, not touched at all.
*/

const (
	base = 0x10000000
	size = 0x4000
	life = base + 0x800

	typeArr = base + 0x1000
	timeArr = base + 0x1400
	slots   = 44 // this build's count, not the 22 of older versions
)

// running is what the planted player already has on.
var running = []struct{ slot, buff, ticks int32 }{
	{slot: 0, buff: 11, ticks: 28800}, // a potion, eight minutes of it
	{slot: 1, buff: 121, ticks: 60},   // something with less left than a renewal gives
	{slot: 3, buff: 122, ticks: 120},  // and exactly as much
}

// plant builds a player with those buffs, and empty slots between them.
func plant(length int32) *memtest.FakeMem {
	mem := memtest.New(base, size)
	mem.PokeBytes(uint32(int64(life)+layoutBuffTypePtr), u32(typeArr)) //nolint:gosec // a delta
	mem.PokeBytes(uint32(int64(life)+layoutBuffTimePtr), u32(timeArr)) //nolint:gosec // a delta
	mem.PokeI32(typeArr+0x0C, length)
	mem.PokeI32(timeArr+0x0C, length)
	for _, r := range running {
		mem.PokeI32(typeArr+0x10+uint32(r.slot)*4, r.buff)  //nolint:gosec // a slot index
		mem.PokeI32(timeArr+0x10+uint32(r.slot)*4, r.ticks) //nolint:gosec // a slot index
	}
	return mem
}

// What is running reads back as what was planted, and the empty slots are left
// out.
func TestActiveIsWhatWasPlanted(t *testing.T) {
	got, err := buffs.New(plant(slots), life).Active()
	require.NoError(t, err)
	require.Len(t, got, len(running), "a different number of buffs is running")

	for i, want := range running {
		require.EqualValuesf(t, want.slot, got[i].Slot, "buff %d is in a different slot", i)
		require.Equalf(t, want.buff, got[i].Type, "slot %d holds a different buff", want.slot)
		require.Equalf(t, want.ticks, got[i].Ticks, "slot %d has a different time", want.slot)
	}
}

/*
How long a buff has left, including for one that is not running.

Zero for a buff that is not on is what the renewal reads to decide between
leaving a potion alone and topping its own effect up.
*/
func TestTimeOf(t *testing.T) {
	bar := buffs.New(plant(slots), life)
	for _, c := range []struct {
		buff, ticks int32
	}{{11, 28800}, {121, 60}, {122, 120}, {999, 0}} {
		require.Equalf(t, c.ticks, bar.TimeOf(c.buff), "buff %d", c.buff)
	}
}

/*
Every renewal does one of four things, and leaves a known set of bytes.

The digest is over the whole buffer, so a renewal that wrote the right number
into the wrong slot fails here -- and a renewal writes into the player's own
buff bar, which is the one thing in this package that is not undoable.
*/
func TestRenew(t *testing.T) {
	for _, c := range []struct {
		name   string
		buff   int32
		ticks  int32
		what   string
		digest string
	}{
		{
			name: "one running far longer, which is a potion", buff: 11, ticks: 120,
			what: buffs.Kept, digest: "7afb13e22ded2e9b",
		},
		{
			name: "one running shorter", buff: 121, ticks: 120,
			what: buffs.Renewed, digest: "6afb9e01efb09ada",
		},
		{
			name: "one running exactly as long", buff: 122, ticks: 120,
			what: buffs.Kept, digest: "7afb13e22ded2e9b",
		},
		{
			name: "one not running at all", buff: 123, ticks: 120,
			what: buffs.Added, digest: "f407428204993260",
		},
		{
			name: "and a longer renewal of the potion", buff: 11, ticks: 99999,
			what: buffs.Renewed, digest: "578938b797fcc0c7",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mem := plant(slots)
			got, err := buffs.New(mem, life).Renew(c.buff, c.ticks)
			require.NoError(t, err)
			require.Equal(t, c.what, got, "the renewal did something else")
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
A potion the player drank is left completely alone.

Not rewritten with the larger of the two values: not touched at all. A write that
happened to store the same number would still be a write, and the rule is about
not reaching for somebody else's buff.
*/
func TestAPotionIsNotTouched(t *testing.T) {
	mem := plant(slots)
	before := mem.Hex()

	what, err := buffs.New(mem, life).Renew(11, 120)
	require.NoError(t, err)
	require.Equal(t, buffs.Kept, what, "a potion running longer was not kept")
	require.Equal(t, before, mem.Hex(), "something was written to a potion's slot")
}

/*
A buff is added with its time first and its type last.

Until the type is set the game ignores the slot, so a half-written one is an
empty slot rather than a buff with no time on it.
*/
func TestABuffIsAddedTimeFirst(t *testing.T) {
	mem := plant(slots)
	watched := &watcher{FakeMem: mem}

	what, err := buffs.New(watched, life).Renew(123, 120)
	require.NoError(t, err)
	require.Equal(t, buffs.Added, what)

	// The first empty slot is 2, between the planted ones.
	timeAt, typeAt := uint32(timeArr+0x10+2*4), uint32(typeArr+0x10+2*4)
	var sawTime bool
	for _, w := range watched.writes {
		if w == timeAt {
			sawTime = true
		}
		if w == typeAt {
			require.True(t, sawTime, "the type was written before the time it goes with")
			return
		}
	}
	require.Fail(t, "the type was never written")
}

// A full bar is said to be full rather than overwriting something.
func TestAFullBar(t *testing.T) {
	mem := plant(slots)
	for i := range int32(slots) {
		mem.PokeI32(typeArr+0x10+uint32(i)*4, 500+i) //nolint:gosec // a slot index
		mem.PokeI32(timeArr+0x10+uint32(i)*4, 60)    //nolint:gosec // a slot index
	}
	before := image(mem)

	got, err := buffs.New(mem, life).Renew(123, 120)
	require.NoError(t, err)
	require.Equal(t, buffs.Full, got, "room was found in a full bar")
	require.Equal(t, before, image(mem), "something was written to a full bar")
}

/*
An array length nobody can believe is refused.

A null or rotted pointer reads as something, and a bar of a hundred thousand
slots is a pointer that is not one -- reading it would walk whatever is there and
renewing would write into it.
*/
func TestAnUnbelievableBarIsRefused(t *testing.T) {
	for _, length := range []int32{0, -1, 257, 1 << 20} {
		_, err := buffs.New(plant(length), life).Active()
		require.Errorf(t, err, "a bar of %d slots was believed", length)
	}
	_, err := buffs.New(plant(slots), life).Active()
	require.NoError(t, err, "a believable bar was refused")
}

// With no player, there is nothing to read rather than something guessed.
func TestNoPlayerLoaded(t *testing.T) {
	_, err := buffs.New(memtest.New(base, size), life).Active()
	require.Error(t, err)
	require.Contains(t, err.Error(), "is a player loaded")
}

// A renewal of nothing, or for no time, is refused rather than written.
func TestNonsenseIsRefused(t *testing.T) {
	bar := buffs.New(plant(slots), life)
	for _, c := range [][2]int32{{0, 120}, {-1, 120}, {121, 0}, {121, -5}} {
		_, err := bar.Renew(c[0], c[1])
		require.Errorf(t, err, "renewing buff %d for %d ticks was allowed", c[0], c[1])
	}
}

// The slot count is this build's, not an older one's.
func TestTheSlotCount(t *testing.T) {
	got, err := buffs.New(plant(slots), life).Slots()
	require.NoError(t, err)
	require.Equal(t, slots, got, "a different number of slots was read")
}
