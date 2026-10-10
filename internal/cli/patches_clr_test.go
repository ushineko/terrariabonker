package cli_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/proc"
)

const (
	clrCode = clrBase + 0x3000
	clrLife = clrBase + 0x800
)

/*
clrResetBlock is the CLR's compiled ResetEffects where both cheats patch, as
found on the live game (spec 052): reach's `mov [esi+0x570], edx` (blockRange),
fld1, mining's `fstp dword [esi+0x514]` (pickSpeed), then the three stores the
anchor recognises.
*/
var clrResetBlock = []byte{
	0x89, 0x96, 0x70, 0x05, 0x00, 0x00, // reach's site
	0xD9, 0xE8, // fld1
	0xD9, 0x9E, 0x14, 0x05, 0x00, 0x00, // mining's site
	0x88, 0x96, 0xD8, 0x08, 0x00, 0x00, 0x88, 0x96, 0xDE, 0x08, 0x00, 0x00,
	0xC6, 0x86, 0xDF, 0x08, 0x00, 0x00, 0x01,
}

/*
clrResetMinions is ResetEffects' `maxMinions = 1; maxTurrets = 1`, and
clrApplyItemTime is ApplyItemTime(Item, float) from the useTime load to the call
after the clamp, as found on the live game (spec 052).
*/
var (
	clrResetMinions = []byte{
		0xC7, 0x86, 0x0C, 0x03, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
		0xC7, 0x86, 0xA8, 0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
	}
	clrPylonCheck = []byte{
		0x0F, 0xB6, 0x44, 0x24, 0x0C, 0x8B, 0x0D, 0x20, 0x5F, 0x29, 0x07, 0x8B, 0xD0, 0x39, 0x09,
		0xE8, 0x44, 0xEC, 0x2B, 0xFE, 0x85, 0xC0, 0x74, 0x08, 0xB8, 0x01, 0x00, 0x00, 0x00,
		0xC2, 0x10, 0x00, 0x33, 0xC0, 0xC2, 0x10, 0x00,
	}
	clrApplyItemTime = []byte{
		0x8B, 0x52, 0x60, 0x89, 0x55, 0xFC, 0xDB, 0x45, 0xFC, 0xD9, 0x5D, 0xFC,
		0xD9, 0x45, 0xFC, 0xD8, 0x4D, 0x08, 0xDD, 0x5D, 0xF4, 0xF2, 0x0F, 0x10,
		0x45, 0xF4, 0xF2, 0x0F, 0x2C, 0xC0,
		0x85, 0xD2, 0x7E, 0x0B, 0x85, 0xC0, 0x7F, 0x07, 0xB8, 0x01, 0x00, 0x00, 0x00, 0xEB, 0x00,
		0x8B, 0xD0, 0xE8, 0x56, 0xFE, 0xFF, 0xFF,
	}
)

const (
	clrMinions  = clrCode + 0x100
	clrItemTime = clrCode + 0x200
	clrPylons   = clrCode + 0x280
)

// clrCoder is the CLR armoury with that code in an executable region.
func clrCoder() *execMem {
	mem := clrArmoury()
	mem.Exec = []proc.Region{{Start: clrCode, End: clrCode + 0x300, Executable: true, Writable: true}}
	mem.PokeBytes(clrCode, clrResetBlock)
	mem.PokeBytes(clrMinions, clrResetMinions)
	mem.PokeBytes(clrItemTime, clrApplyItemTime)
	mem.PokeBytes(clrPylons, clrPylonCheck)
	return mem
}

/*
Under .NET Framework the minion cap and fast placement write their values into
the CLR's code -- the cap as maxMinions' immediate, the placement speed as `mov
eax, N` over the clamp -- and turning them off puts the code back exactly.
*/
func TestUnderTheCLRMinionsAndPlacementArePatched(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrCoder()

	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "max_minions", "--value", "12")
	require.Zero(t, code, errOut)
	require.Equal(t, []byte{12, 0, 0, 0}, mem.Read(clrMinions+6, 4))

	code, _, errOut = runUnder(t, runtime, mem, "patch", "enable", "fast_place", "--value", "2")
	require.Zero(t, code, errOut)
	require.Equal(t, []byte{0xB8, 2, 0, 0, 0, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		mem.Read(clrItemTime+30, 15))

	for _, cheat := range []string{"max_minions", "fast_place"} {
		code, _, errOut = runUnder(t, runtime, mem, "patch", "disable", cheat)
		require.Zero(t, code, errOut)
	}
	require.Equal(t, clrResetMinions, mem.Read(clrMinions, len(clrResetMinions)))
	require.Equal(t, clrApplyItemTime, mem.Read(clrItemTime, len(clrApplyItemTime)))
}

// Under .NET Framework the pylon check returns "can place" at once, and turning
// it off puts its first instruction back.
func TestUnderTheCLRPylonsArePatched(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrCoder()
	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "pylons")
	require.Zero(t, code, errOut)
	require.Equal(t, []byte{0x33, 0xC0, 0xC2, 0x10, 0x00}, mem.Read(clrPylons, 5))
	code, _, errOut = runUnder(t, runtime, mem, "patch", "disable", "pylons")
	require.Zero(t, code, errOut)
	require.Equal(t, clrPylonCheck, mem.Read(clrPylons, len(clrPylonCheck)))
}

/*
A site that holds neither the original bytes nor the cheat's own patch is not
written, on or off. The anchor wildcards the bytes a cheat writes, so matching it
says nothing about them: here mining's site holds some other instruction.
*/
func TestUnderTheCLRAnUnexpectedSiteIsNotWritten(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrCoder()
	mem.PokeBytes(clrCode+8, []byte{0xD9, 0x9E, 0x18, 0x05, 0x00, 0x00}) // fstp [esi+0x518]
	before := mem.Read(clrBase, 0x20000)

	for _, action := range []string{"enable", "disable"} {
		code, _, errOut := runUnder(t, runtime, mem, "patch", action, "mining")
		require.NotZero(t, code, action)
		require.Contains(t, errOut, "neither the original nor this cheat's patch", action)
		require.Equal(t, before, mem.Read(clrBase, 0x20000), "%s wrote to the game", action)
	}
}

/*
Under .NET Framework mining and reach patch the CLR's code at the CLR's sites and
set the CLR's fields, and turning them off puts back exactly what was there.
*/
func TestUnderTheCLRMiningAndReachArePatched(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrCoder()

	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "mining")
	require.Zero(t, code, errOut)
	require.Equal(t, []byte{0xDD, 0xD8, 0x90, 0x90, 0x90, 0x90}, mem.Read(clrCode+8, 6))
	bits, _ := mem.ReadI32(clrLife + 0xA4)
	require.InDelta(t, 0.2, math.Float32frombits(uint32(bits)), 1e-6, "pickSpeed") //nolint:gosec // float bits

	code, _, errOut = runUnder(t, runtime, mem, "patch", "enable", "reach", "--value", "30")
	require.Zero(t, code, errOut)
	require.Equal(t, []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90}, mem.Read(clrCode, 6))
	reach, _ := mem.ReadI32(clrLife + 0x100)
	require.EqualValues(t, 30, reach, "blockRange")

	for _, cheat := range []string{"mining", "reach"} {
		code, _, errOut = runUnder(t, runtime, mem, "patch", "disable", cheat)
		require.Zero(t, code, errOut)
	}
	require.Equal(t, clrResetBlock, mem.Read(clrCode, len(clrResetBlock)))
}

/*
A cheat with no CLR site is reported as missing under the CLR, not as broken,
and turning it on is refused without touching the game.
*/
func TestUnderTheCLRACheatWithoutASiteIsRefused(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrCoder()
	before := mem.Read(clrBase, 0x20000)

	code, out, errOut := runUnder(t, runtime, mem, "patch", "status")
	require.Zero(t, code, errOut)
	require.Contains(t, out, "auto_use has no code site under netfx-4.8.1 yet")

	code, _, errOut = runUnder(t, runtime, mem, "patch", "enable", "auto_use")
	require.NotZero(t, code)
	require.Contains(t, errOut, "nothing was changed")
	require.Equal(t, before, mem.Read(clrBase, 0x20000), "the game was written to")
}
