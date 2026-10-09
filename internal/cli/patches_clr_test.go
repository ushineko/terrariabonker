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

// clrCoder is the CLR armoury with that code in an executable region.
func clrCoder() *execMem {
	mem := clrArmoury()
	mem.Exec = []proc.Region{{Start: clrCode, End: clrCode + 0x100, Executable: true, Writable: true}}
	mem.PokeBytes(clrCode, clrResetBlock)
	return mem
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
	require.Contains(t, out, "max_minions has no code site under netfx-4.8.1 yet")

	code, _, errOut = runUnder(t, runtime, mem, "patch", "enable", "max_minions")
	require.NotZero(t, code)
	require.Contains(t, errOut, "nothing was changed")
	require.Equal(t, before, mem.Read(clrBase, 0x20000), "the game was written to")
}
