package buffs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

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

// pyPlant is the same, as Python source.
func pyPlant(length int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
import json, os, struct, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import buffs as B
mem = FakeMem(%d, %d)
mem.poke_bytes(%d + B.BUFF_TYPE_PTR_OFF, struct.pack("<I", %d))
mem.poke_bytes(%d + B.BUFF_TIME_PTR_OFF, struct.pack("<I", %d))
mem.poke_i32(%d + 0x0C, %d)
mem.poke_i32(%d + 0x0C, %d)
`, base, size, life, typeArr, life, timeArr, typeArr, length, timeArr, length)
	for _, r := range running {
		fmt.Fprintf(&b, "mem.poke_i32(%d + 0x10 + %d * 4, %d)\n", typeArr, r.slot, r.buff)
		fmt.Fprintf(&b, "mem.poke_i32(%d + 0x10 + %d * 4, %d)\n", timeArr, r.slot, r.ticks)
	}
	fmt.Fprintf(&b, "bar = B.Buffs(mem, %d)\n", life)
	return b.String()
}

// What is running reads the same, and the empty slots are left out.
func TestActiveMatchesThePython(t *testing.T) {
	var want map[string][]int32
	askPython(t, pyPlant(slots)+`
print(json.dumps({str(k): list(v) for k, v in bar.active().items()}))`, &want)

	got, err := buffs.New(plant(slots), life).Active()
	require.NoError(t, err)
	require.Len(t, got, len(want), "a different number of buffs is running")
	for _, a := range got {
		w, known := want[fmt.Sprintf("%d", a.Slot)]
		require.Truef(t, known, "slot %d is running there and not here", a.Slot)
		require.Equalf(t, w[0], a.Type, "slot %d holds a different buff", a.Slot)
		require.Equalf(t, w[1], a.Ticks, "slot %d has a different time", a.Slot)
	}
}

// How long a buff has left is the same, including for one that is not running.
func TestTimeOfMatchesThePython(t *testing.T) {
	asked := []int32{11, 121, 122, 999}
	var want []int32
	askPython(t, pyPlant(slots)+fmt.Sprintf(
		"print(json.dumps([bar.time_of(b) for b in [%d, %d, %d, %d]]))",
		asked[0], asked[1], asked[2], asked[3]), &want)

	bar := buffs.New(plant(slots), life)
	for i, b := range asked {
		require.Equalf(t, want[i], bar.TimeOf(b), "buff %d has a different time left", b)
	}
}

// Every renewal does the same thing and leaves the same bytes.
func TestRenewMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name  string
		buff  int32
		ticks int32
	}{
		{"one running far longer, which is a potion", 11, 120},
		{"one running shorter", 121, 120},
		{"one running exactly as long", 122, 120},
		{"one not running at all", 123, 120},
		{"and a longer renewal of the potion", 11, 99999},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				What string `json:"what"`
				Buf  string `json:"buf"`
			}
			askPython(t, pyPlant(slots)+fmt.Sprintf(`
what = bar.renew(%d, %d)
print(json.dumps({"what": what, "buf": mem.buf.hex()}))`, c.buff, c.ticks), &want)

			mem := plant(slots)
			got, err := buffs.New(mem, life).Renew(c.buff, c.ticks)
			require.NoError(t, err)
			require.Equal(t, want.What, got, "the renewal did something else")
			require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
		})
	}
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
func TestAFullBarMatchesThePython(t *testing.T) {
	var want string
	askPython(t, pyPlant(slots)+`
for i in range(bar.slots()):
    mem.poke_i32(`+fmt.Sprintf("%d", typeArr)+` + 0x10 + i * 4, 500 + i)
    mem.poke_i32(`+fmt.Sprintf("%d", timeArr)+` + 0x10 + i * 4, 60)
print(json.dumps(bar.renew(123, 120)))`, &want)
	require.Equal(t, buffs.Full, want, "the Python found room in a full bar")

	mem := plant(slots)
	for i := range int32(slots) {
		mem.PokeI32(typeArr+0x10+uint32(i)*4, 500+i) //nolint:gosec // a slot index
		mem.PokeI32(timeArr+0x10+uint32(i)*4, 60)    //nolint:gosec // a slot index
	}
	before := mem.Hex()

	got, err := buffs.New(mem, life).Renew(123, 120)
	require.NoError(t, err)
	require.Equal(t, buffs.Full, got)
	require.Equal(t, before, mem.Hex(), "something was written to a full bar")
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
func TestTheSlotCountMatchesThePython(t *testing.T) {
	var want int
	askPython(t, pyPlant(slots)+"print(json.dumps(bar.slots()))", &want)

	got, err := buffs.New(plant(slots), life).Slots()
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, slots, got, "and not the count the fixture plants")
}
