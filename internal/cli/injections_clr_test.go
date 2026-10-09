package cli_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The CLR's code at the three hooks, as found on the live game (spec 052): the
tail of GetItemGrabRange, the tail of GetSpawnRate, and two of TryDroppingItem's
twins, which differ in the argument they pass the roll. Call targets and
constant addresses are zeroed: the anchors wildcard them.
*/
var (
	clrGrabRangeTail = []byte{
		0xFF, 0x15, 0, 0, 0, 0, 0x85, 0xC0, 0x74, 0x06, 0x81, 0xC7, 0xF0, 0x00, 0x00, 0x00,
		0x8B, 0xC7, 0x5B, 0x5E, 0x5F, 0x5D, 0xC3,
	}
	clrSpawnRateTail = append(append([]byte{
		0x85, 0xC0, 0x75, 0x38},
		bytes.Repeat([]byte{
			0xDB, 0x06, 0xD9, 0x5D, 0xD8, 0xD9, 0x45, 0xD8, 0xD8, 0x0D, 0, 0, 0, 0, 0xDD, 0x5D, 0xD0,
			0xF2, 0x0F, 0x10, 0x45, 0xD0, 0xF2, 0x0F, 0x2C, 0xC0, 0x89, 0x06}, 1)...),
		0xDB, 0x07, 0xD9, 0x5D, 0xD8, 0xD9, 0x45, 0xD8, 0xD8, 0x0D, 0, 0, 0, 0, 0xDD, 0x5D, 0xD0,
		0xF2, 0x0F, 0x10, 0x45, 0xD0, 0xF2, 0x0F, 0x2C, 0xC0, 0x89, 0x07,
		0x8D, 0x65, 0xF4, 0x5B, 0x5E, 0x5F, 0x5D, 0xC2, 0x08, 0x00)
	clrTryDrop = func(arg byte) []byte {
		return []byte{0x55, 0x8B, 0xEC, 0x57, 0x56, 0x53, 0x8B, 0xF1, 0x8B, 0x56, 0x0C, 0x8B, 0x4D, arg,
			0x39, 0x09, 0xE8, 0, 0, 0, 0, 0x3B, 0x46, 0x18, 0x7D, 0x30, 0x8B, 0x7D, 0x08,
			0x8B, 0x5E, 0x08, 0x8B, 0x56, 0x10, 0x8B, 0x46, 0x14}
	}
)

const (
	clrGrab   = clrCode + 0x300
	clrSpawn  = clrCode + 0x380
	clrDropA  = clrCode + 0x400
	clrDropB  = clrCode + 0x480
	clrHookTo = clrCode + 0x500
)

// clrStubbed is the CLR coder with the three hooks' code planted, and memory
// this program can allocate an arena in.
func clrStubbed() *execMem {
	mem := clrCoder()
	mem.allocates = true
	mem.Exec = []proc.Region{{Start: clrCode, End: clrHookTo, Executable: true, Writable: true}}
	mem.PokeBytes(clrGrab, clrGrabRangeTail)
	mem.PokeBytes(clrSpawn, clrSpawnRateTail)
	mem.PokeBytes(clrDropA, clrTryDrop(0x0C))
	mem.PokeBytes(clrDropB, clrTryDrop(0x10))
	return mem
}

// stubAt follows the jump at a hook site to its stub and returns the stub's
// body of n bytes, and where the jump home that must follow it lands.
func stubAt(t *testing.T, mem *execMem, site uint32, n int) ([]byte, uint32) {
	t.Helper()
	jmp := mem.Read(site, 5)
	require.Equal(t, byte(0xE9), jmp[0], "no jump at %#x", site)
	stub := site + 5 + binary.LittleEndian.Uint32(jmp[1:])
	code := mem.Read(stub, n+5)
	require.Equal(t, byte(0xE9), code[n], "no jump home after the body")
	return code[:n], stub + uint32(n) + 5 + binary.LittleEndian.Uint32(code[n+1:]) //nolint:gosec // inside a slot
}

/*
Under .NET Framework the arena is allocated in the game directly, and pickup,
spawn rate and the drop floor hook the CLR's code: each site jumps to a stub that
runs the CLR body and jumps back past the displaced bytes; the drop floor goes
into every twin. Turning them off puts every site back.
*/
func TestUnderTheCLRTheStubsAreInstalled(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrStubbed()
	for _, on := range [][]string{{"pickup", "5"}, {"spawn_rate", "0"}, {"loot", "50"}} {
		code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", on[0], "--value", on[1])
		require.Zero(t, code, errOut)
	}
	require.Len(t, mem.mapped, 1, "the arena was not allocated, or allocated twice")

	body, home := stubAt(t, mem, clrGrab+16, 8)
	require.Equal(t, []byte{0x6B, 0xFF, 0x05, 0x8B, 0xC7, 0x5B, 0x5E, 0x5F}, body, "imul edi,edi,5 then the epilogue")
	require.Equal(t, uint32(clrGrab+21), home)

	body, home = stubAt(t, mem, clrSpawn+60, 17)
	require.Equal(t, []byte{0xC7, 0x06, 6, 0, 0, 0, 0xC7, 0x07, 0, 0, 0, 0, 0x8D, 0x65, 0xF4, 0x5B, 0x5E}, body)
	require.Equal(t, uint32(clrSpawn+65), home)

	for _, drop := range []uint32{clrDropA, clrDropB} {
		body, home = stubAt(t, mem, drop+6, 18)
		require.Equal(t, []byte{0x8B, 0xF1, 0x8B, 0x56, 0x0C, 0x81, 0xFA, 2, 0, 0, 0, 0x7E, 0x05, 0xBA, 2, 0, 0, 0}, body,
			"the denominator capped at 2")
		require.Equal(t, drop+11, home)
	}

	for _, off := range []string{"pickup", "spawn_rate", "loot"} {
		code, _, errOut := runUnder(t, runtime, mem, "patch", "disable", off)
		require.Zero(t, code, errOut)
	}
	require.Equal(t, clrGrabRangeTail, mem.Read(clrGrab, len(clrGrabRangeTail)))
	require.Equal(t, clrSpawnRateTail, mem.Read(clrSpawn, len(clrSpawnRateTail)))
	require.Equal(t, clrTryDrop(0x0C), mem.Read(clrDropA, len(clrTryDrop(0x0C))))
	require.Equal(t, clrTryDrop(0x10), mem.Read(clrDropB, len(clrTryDrop(0x10))))
}
