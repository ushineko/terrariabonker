package locate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

// clrLocator is the locator for the .NET Framework entry, as Select hands it
// out for the measured build and runtime.
func clrLocator(t *testing.T) locate.Locator {
	t.Helper()
	e, support := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	require.Equal(t, layout.Supported, support)
	return locate.With(e)
}

/*
A CLR player is found by the CLR locator, with its caps the right way round.

The block is planted in storage order -- statLifeMax 400 first, statLifeMax2 420
second -- with mana boosted too, so a locator reading the first two words the
mono way would either reject the block (a boosted cap below the permanent one)
or report the caps swapped.
*/
func TestTheCLRLocatorFindsACLRPlayer(t *testing.T) {
	const base, size = 0x10000000, 0x4000
	mem := memtest.New(base, size)
	mem.PlantCLRString(base+0x40, "terrariabonker")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 420, 400, 200, 200, 220}, base+0x40)

	got := clrLocator(t).FindPlayers(mem)
	require.Len(t, got, 1)
	require.EqualValues(t, base+0x800, got[0].LifeAddr)
	require.Equal(t, "terrariabonker", got[0].Name)
	require.EqualValues(t, 400, got[0].StatLifeMax, "the permanent cap")
	require.EqualValues(t, 420, got[0].StatLifeMax2, "the boosted cap")
	require.EqualValues(t, 400, got[0].StatLife)
	require.EqualValues(t, 220, got[0].StatManaMax2)

	blk, ok := clrLocator(t).ReadBlock(mem, base+0x800)
	require.True(t, ok)
	require.Equal(t, got[0], blk, "a re-read of the found address disagrees with the scan")
}

/*
Each runtime's locator finds only its own runtime's players.

This is the fail-safe the whole port leans on: a reader handed the wrong entry
finds nothing, rather than something.
*/
func TestEachLocatorFindsOnlyItsOwnRuntimesPlayers(t *testing.T) {
	const base, size = 0x10000000, 0x8000
	mem := memtest.New(base, size)
	mem.PlantCLRString(base+0x40, "clr")
	mem.PlantMonoString(base+0x80, "mono")
	mem.PlantCLRPlayer(base+0x1000, []int32{400, 400, 400, 200, 200, 200}, base+0x40)
	mem.PlantPlayer(base+0x4000, []int32{400, 400, 400, 200, 200, 200}, base+0x80)

	clr := clrLocator(t).FindPlayers(mem)
	require.Len(t, clr, 1)
	require.Equal(t, "clr", clr[0].Name)

	mono := locate.FindPlayers(mem)
	require.Len(t, mono, 1)
	require.Equal(t, "mono", mono[0].Name)

	_, ok := clrLocator(t).ReadBlock(mem, base+0x4000)
	require.False(t, ok, "the CLR locator read a mono player")
	_, ok = locate.ReadBlock(mem, base+0x1000)
	require.False(t, ok, "the mono locator read a CLR player")
}

// The CLR string shape: length at +4, characters at +8.
func TestTheCLRStringShape(t *testing.T) {
	const base, size = 0x10000000, 0x1000
	mem := memtest.New(base, size)
	mem.PlantCLRString(base+0x100, "pooneau")
	got, ok := clrLocator(t).ReadString(mem, base+0x100)
	require.True(t, ok)
	require.Equal(t, "pooneau", got)

	_, ok = locate.ReadMonoString(mem, base+0x100)
	require.False(t, ok, "a CLR string read as a mono one")
}

/*
A CLR player whose boosted life cap is above 500 is still found.

The scan prefilters on the permanent cap, which is 100..500. Under the CLR that
is the first word of the block; a prefilter reading the second would see the
boosted cap and drop a player whose accessories lift it past 500.
*/
func TestTheCLRPrefilterReadsThePermanentCap(t *testing.T) {
	const base, size = 0x10000000, 0x4000
	mem := memtest.New(base, size)
	mem.PlantCLRString(base+0x40, "buffed")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 520, 400, 200, 200, 200}, base+0x40)

	got := clrLocator(t).FindPlayers(mem)
	require.Len(t, got, 1)
	require.EqualValues(t, 520, got[0].StatLifeMax2)
}

func monoEntry(t *testing.T) layout.Entry {
	t.Helper()
	e, _ := layout.Select(layout.Build1458s24893155, "wine-mono-11.3.0")
	return e
}

// A buffed CLR player -- life above the permanent cap, within the boosted one --
// is found, as under mono.
func TestABuffedCLRPlayerIsFound(t *testing.T) {
	const base, size = 0x10000000, 0x4000
	mem := memtest.New(base, size)
	mem.PlantCLRString(base+0x40, "lifeforce")
	mem.PlantCLRPlayer(base+0x800, []int32{400, 500, 480, 200, 200, 200}, base+0x40)

	got := clrLocator(t).FindPlayers(mem)
	require.Len(t, got, 1, "a player above their permanent life cap was not found")
	require.EqualValues(t, 480, got[0].StatLife)
}
