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
	clrTriggerPing = []byte{
		0x55, 0x8B, 0xEC, 0x56, 0x83, 0xEC, 0x0C, 0x33, 0xC0, 0x89, 0x45, 0xF0,
		0x80, 0x3D, 0, 0, 0, 0, 0x00, 0x74, 0x08,
		0x8D, 0x65, 0xFC, 0x5E, 0x5D, 0xC2, 0x08, 0x00, 0x8D, 0x45, 0x08,
	}
	clrInventoryScan = []byte{
		0x8B, 0x86, 0xD4, 0x00, 0x00, 0x00, 0x3B, 0x58, 0x04, 0x0F, 0x83, 0, 0, 0, 0,
		0x8B, 0x44, 0x98, 0x08, 0x8B, 0x78, 0x50, 0x8B, 0xCE, 0x8B, 0xD7, 0xE8, 0, 0, 0, 0,
	}
	clrApplyFuncEntry = []byte{
		0x55, 0x8B, 0xEC, 0x57, 0x56, 0x53, 0x81, 0xEC, 0x08, 0x01, 0x00, 0x00,
		0x33, 0xC0, 0x89, 0x85, 0x1C, 0xFF, 0xFF, 0xFF,
	}
	clrGrantPrefixEntry = []byte{
		0x55, 0x8B, 0xEC, 0x80, 0xBA, 0x2E, 0x01, 0x00, 0x00, 0x3E, 0x75, 0x06,
		0xFF, 0x81, 0x64, 0x04, 0x00, 0x00, 0x80, 0xBA, 0x2E, 0x01, 0x00, 0x00, 0x3F,
	}
	clrGrantArmorEntry = []byte{
		0x55, 0x8B, 0xEC, 0x57, 0x56, 0x53, 0x8B, 0xF1, 0x8B, 0xFA, 0x8B, 0x5F, 0x50,
		0x8B, 0xCE, 0x8B, 0xD3, 0xE8, 0, 0, 0, 0, 0x8B, 0xCE, 0x8B, 0xD3, 0xE8,
	}
	clrPlayerTeleport = []byte{
		0xC7, 0x83, 0xA4, 0x06, 0x00, 0x00, 0x64, 0x00, 0x00, 0x00,
		0xC7, 0x83, 0x54, 0x03, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00,
	}
	clrTryDrop = func(arg byte) []byte {
		return []byte{0x55, 0x8B, 0xEC, 0x57, 0x56, 0x53, 0x8B, 0xF1, 0x8B, 0x56, 0x0C, 0x8B, 0x4D, arg,
			0x39, 0x09, 0xE8, 0, 0, 0, 0, 0x3B, 0x46, 0x18, 0x7D, 0x30, 0x8B, 0x7D, 0x08,
			0x8B, 0x5E, 0x08, 0x8B, 0x56, 0x10, 0x8B, 0x46, 0x14}
	}
)

/*
clrGetRanges is GetRanges from its entry to its exit 0xF1 in, the code between
stood in for by nops; clrSmartCursor is SmartCursorLookup from the endY store
through the in-reach test.
*/
var (
	clrGetRanges = append(append([]byte{
		0x55, 0x8B, 0xEC, 0x57, 0x56, 0x53, 0x83, 0xEC, 0x08, 0x8B, 0xF1, 0x8B, 0xFA,
		0xA1, 0, 0, 0, 0, 0x0F, 0xAF, 0x06, 0x89, 0x07,
		0xA1, 0, 0, 0, 0, 0x0F, 0xAF, 0x06, 0x8B, 0x55, 0x08, 0x89, 0x02},
		bytes.Repeat([]byte{0x90}, 0xF1-36)...),
		0x8D, 0x65, 0xF4, 0x5B, 0x5E, 0x5F, 0x5D, 0xC2, 0x04, 0x00)
	clrSmartCursor = []byte{
		0x89, 0x43, 0x20, 0x83, 0x7D, 0xC4, 0x00, 0x74, 0x28,
		0x8B, 0x43, 0x0C, 0x3B, 0x43, 0x14, 0x7C, 0x20, 0x8B, 0x43, 0x0C, 0x3B, 0x43, 0x18, 0x7F, 0x18,
		0x8B, 0x43, 0x10, 0x3B, 0x43, 0x1C, 0x7C, 0x10, 0x8B, 0x43, 0x10, 0x3B, 0x43, 0x20, 0x7F, 0x08,
	}
)

/*
clrEffectsLoop and clrBenefitLoop are UpdateEquips' two accessory loops: the
effects loop from `mov ebx, 3` through its `k < 10` bound, and the benefit loop
from the accessory test through its own.
*/
var (
	clrEffectsLoop = []byte{
		0xBB, 0x03, 0x00, 0x00, 0x00, 0x8B, 0xCE, 0x8B, 0xD3, 0xE8, 0, 0, 0, 0, 0x85, 0xC0, 0x74, 0x23,
		0x8D, 0x7D, 0xE0, 0x0F, 0x57, 0xC0, 0x66, 0x0F, 0xD6, 0x07, 0x8D, 0x45, 0xE0, 0x50, 0x8B, 0xCE,
		0x8B, 0xD3, 0xFF, 0x15, 0, 0, 0, 0, 0x50, 0x8B, 0xD3, 0x8B, 0xCE, 0xFF, 0x15, 0, 0, 0, 0,
		0x43, 0x83, 0xFB, 0x0A, 0x7C, 0xCA,
	}
	clrBenefitLoop = []byte{
		0x80, 0xBF, 0x0E, 0x01, 0x00, 0x00, 0x00, 0x74, 0x0A, 0x8B, 0xCE, 0x8B, 0xD7, 0xFF, 0x15, 0, 0, 0, 0,
		0x8B, 0xCE, 0x8B, 0xD7, 0xFF, 0x15, 0, 0, 0, 0, 0x43, 0x83, 0xFB, 0x0A, 0x0F, 0x8C,
	}
)

const (
	clrGrab     = clrCode + 0x300
	clrSpawn    = clrCode + 0x380
	clrDropA    = clrCode + 0x400
	clrDropB    = clrCode + 0x480
	clrRanges   = clrCode + 0x500
	clrSmart    = clrCode + 0x600
	clrEffects  = clrCode + 0x700
	clrBenefits = clrCode + 0x780
	clrTrigger  = clrCode + 0x800
	clrTpEntry  = clrCode + 0x880 // Player.Teleport entry; its anchor sits 0x21 in
	clrInvScan  = clrCode + 0x900
	clrApplyFn  = clrCode + 0xA00
	clrPrefixFn = clrCode + 0xA80
	clrArmorFn  = clrCode + 0xB00
	clrHookTo   = clrCode + 0xB80
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
	mem.PokeBytes(clrRanges, clrGetRanges)
	mem.PokeBytes(clrSmart, clrSmartCursor)
	mem.PokeBytes(clrEffects, clrEffectsLoop)
	mem.PokeBytes(clrBenefits, clrBenefitLoop)
	mem.PokeBytes(clrTrigger, clrTriggerPing)
	mem.PokeBytes(clrTpEntry+0x21, clrPlayerTeleport) // the anchor, 0x21 past the entry
	mem.PokeBytes(clrInvScan, clrInventoryScan)
	mem.PokeBytes(clrApplyFn, clrApplyFuncEntry)
	mem.PokeBytes(clrPrefixFn, clrGrantPrefixEntry)
	mem.PokeBytes(clrArmorFn, clrGrantArmorEntry)
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
	for _, on := range [][]string{{"pickup", "5"}, {"spawn_rate", "0"}, {"loot", "50"},
		{"tool_reach", "30"}, {"smart_cursor", "20"}, {"vanity_accs", "1"}} {
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

	body, home = stubAt(t, mem, clrRanges+0xF1, 20)
	require.Equal(t, []byte{0xC7, 0x07, 30, 0, 0, 0, 0x8B, 0x45, 0x08, 0xC7, 0x00, 30, 0, 0, 0,
		0x8D, 0x65, 0xF4, 0x5B, 0x5E}, body, "x through edi, y through [ebp+8], then the epilogue")
	require.Equal(t, uint32(clrRanges+0xF1+5), home)

	smart := patchBody(t, "smart_cursor", 20)
	body, home = stubAt(t, mem, clrSmart, len(smart))
	require.Equal(t, smart, body)
	require.Equal(t, uint32(clrSmart+7), home, "back to the jz after the displaced compare")

	body, home = stubAt(t, mem, clrEffects+42, 13)
	require.Equal(t, []byte{0x50, 0x8B, 0xD3, 0x83, 0xFA, 0x0A, 0x7C, 0x03, 0x83, 0xEA, 0x0A, 0x8B, 0xCE}, body,
		"push the item, the slot clamped in edx, the player in ecx")
	require.Equal(t, uint32(clrEffects+47), home, "back to the call")
	require.Equal(t, []byte{0x14}, mem.Read(clrEffects+56, 1), "the effects loop runs to 20")
	require.Equal(t, []byte{0x14}, mem.Read(clrBenefits+32, 1), "the benefit loop runs to 20")

	for _, off := range []string{"pickup", "spawn_rate", "loot", "tool_reach", "smart_cursor", "vanity_accs"} {
		code, _, errOut := runUnder(t, runtime, mem, "patch", "disable", off)
		require.Zero(t, code, errOut)
	}
	require.Equal(t, clrGrabRangeTail, mem.Read(clrGrab, len(clrGrabRangeTail)))
	require.Equal(t, clrSpawnRateTail, mem.Read(clrSpawn, len(clrSpawnRateTail)))
	require.Equal(t, clrTryDrop(0x0C), mem.Read(clrDropA, len(clrTryDrop(0x0C))))
	require.Equal(t, clrTryDrop(0x10), mem.Read(clrDropB, len(clrTryDrop(0x10))))
	require.Equal(t, clrGetRanges, mem.Read(clrRanges, len(clrGetRanges)))
	require.Equal(t, clrSmartCursor, mem.Read(clrSmart, len(clrSmartCursor)))
	require.Equal(t, clrEffectsLoop, mem.Read(clrEffects, len(clrEffectsLoop)))
	require.Equal(t, clrBenefitLoop, mem.Read(clrBenefits, len(clrBenefitLoop)))
}

/*
Under .NET Framework inventory accessories hooks the inventory loop and its stub
calls GrantPrefixBenefits, GrantArmorBenefits and ApplyEquipFunctional -- the
three method entries resolved by their own anchors. Disabling restores the loop.
*/
func TestUnderTheCLRInventoryAccessoriesAreInstalled(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrStubbed()
	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "inventory_accs")
	require.Zero(t, code, errOut)

	jmp := mem.Read(clrInvScan+19, 5)
	require.Equal(t, byte(0xE9), jmp[0], "no jump at the inventory-scan hook")
	stub := clrInvScan + 19 + 5 + binary.LittleEndian.Uint32(jmp[1:])
	body := mem.Read(stub, 80)

	// the three call targets, in order, as mov eax,imm32 (B8) immediates
	var targets []uint32
	for i := 0; i+5 <= len(body); i++ {
		if body[i] == 0xB8 && i+4 < len(body) && body[i+5] == 0xFF && body[i+6] == 0xD0 {
			targets = append(targets, binary.LittleEndian.Uint32(body[i+1:]))
		}
	}
	require.Equal(t, []uint32{clrPrefixFn, clrArmorFn, clrApplyFn}, targets,
		"the stub calls prefix, armor, then apply-functional")

	code, _, errOut = runUnder(t, runtime, mem, "patch", "disable", "inventory_accs")
	require.Zero(t, code, errOut)
	require.Equal(t, clrInventoryScan, mem.Read(clrInvScan, len(clrInventoryScan)))
}

/*
Under .NET Framework teleport hooks TriggerPing at offset 3 and its stub calls
Player.Teleport -- the method entry resolved as its anchor less 0x21 -- with the
live player baked in. Disabling restores the hook.
*/
func TestUnderTheCLRTeleportIsInstalled(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrStubbed()
	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "teleport")
	require.Zero(t, code, errOut)

	// the hook is the 5-byte jmp at TriggerPing+3
	jmp := mem.Read(clrTrigger+3, 5)
	require.Equal(t, byte(0xE9), jmp[0], "no jump at the TriggerPing hook")
	stub := clrTrigger + 3 + 5 + binary.LittleEndian.Uint32(jmp[1:])

	body := mem.Read(stub, 48)
	require.Equal(t, byte(0x60), body[0], "stub does not start with pushad")
	// the call target immediate (mov eax, imm32 == B8) must be the Teleport entry
	i := bytes.IndexByte(body, 0xB8)
	require.GreaterOrEqual(t, i, 0, "no mov eax,target in the stub")
	target := binary.LittleEndian.Uint32(body[i+1:])
	require.EqualValues(t, clrTpEntry, target, "the call target is not Teleport's entry (anchor - 0x21)")

	code, _, errOut = runUnder(t, runtime, mem, "patch", "disable", "teleport")
	require.Zero(t, code, errOut)
	require.Equal(t, clrTriggerPing, mem.Read(clrTrigger, len(clrTriggerPing)))
}

/*
A cheat with an edit is refused whole when an edit's site holds something
unexpected: nothing is written, not the edit, not the stub, not the jump. The
benefit loop's bound here is neither 10 nor 20.
*/
func TestUnderTheCLRAnUnexpectedEditRefusesTheWholeCheat(t *testing.T) {
	const runtime = "netfx-4.8.9345.0"
	mem := clrStubbed()
	mem.PokeBytes(clrBenefits+32, []byte{0x0B})
	before := mem.Read(clrBase, 0x80000)

	code, _, errOut := runUnder(t, runtime, mem, "patch", "enable", "vanity_accs")
	require.NotZero(t, code)
	require.Contains(t, errOut, "neither the original nor the edit")
	require.Equal(t, before, mem.Read(clrBase, 0x80000), "something was written")
}

/*
patchBody is the smart-cursor stub as the CLR set builds it, from the parts this
test can state: the displaced store first, the CLR compare last, and between them
the two axis clamps on ebx -- each mov eax,[ebx+start]; add eax,[ebx+end]; sar;
mov ecx,[ebx+target]... with n in both immediates.
*/
func patchBody(t *testing.T, _ string, n int32) []byte {
	t.Helper()
	imm := binary.LittleEndian.AppendUint32(nil, uint32(n)) //nolint:gosec // a small value
	axis := func(target, start, end byte) []byte {
		out := []byte{0x8B, 0x43, start, 0x03, 0x43, end, 0xD1, 0xF8, 0x8B, 0x4B, target,
			0x3B, 0xC1, 0x7E, 0x01, 0x91, 0x2D}
		out = append(out, imm...)
		out = append(append(out, 0x81, 0xC1), imm...)
		return append(out, 0x3B, 0x43, start, 0x7E, 0x03, 0x89, 0x43, start,
			0x3B, 0x4B, end, 0x7D, 0x03, 0x89, 0x4B, end)
	}
	out := []byte{0x89, 0x43, 0x20}
	out = append(out, axis(0x0C, 0x14, 0x18)...)
	out = append(out, axis(0x10, 0x1C, 0x20)...)
	return append(out, 0x83, 0x7D, 0xC4, 0x00)
}
