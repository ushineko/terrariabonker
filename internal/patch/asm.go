package patch

import "encoding/binary"

/*
The instruction fragments the cheats are built from.

Each is a few bytes of x86 assembled by hand, and each is commented with what it
assembles to, because that is the only readable form: a mistake here is not a
compile error, it is a corrupted game.
*/

// i32 packs a signed word, which is how the game's own immediates are written.
func i32(n int32) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(n)) //nolint:gosec // a word, as its bits
}

/*
u32 packs an absolute address.

Unsigned, because a high user-space address in a 32-bit process does not fit a
signed word and packing one as signed would overflow.
*/
func u32(n uint32) []byte { return binary.LittleEndian.AppendUint32(nil, n) }

/*
ForceXY is `mov dword [esi],N; mov dword [edi],N`.

It forces both of GetRanges' outputs, which are still in esi and edi at the point
this runs, past the clamp the game applies to them.
*/
func ForceXY(n int32) []byte {
	out := append([]byte{0xC7, 0x06}, i32(n)...)
	return append(append(out, 0xC7, 0x07), i32(n)...)
}

/*
ImulEAX is `imul eax, eax, N`, scaling whatever is in eax.

The short encoding is used when the multiplier fits a byte, which is what the
assembler would do and what keeps the stub inside its slot.
*/
func ImulEAX(n int32) []byte {
	if n >= -128 && n <= 127 {
		return []byte{0x6B, 0xC0, byte(n)} //nolint:gosec // range-checked above
	}
	return append([]byte{0x69, 0xC0}, i32(n)...)
}

/*
ForceSpawn is `mov [esi],6; mov [edi],N`.

GetSpawnRate's two outputs: a low spawn rate, which means frequent, and a cap on
how many enemies may be active at once. A cap of zero is a peaceful world.
*/
func ForceSpawn(n int32) []byte {
	out := append([]byte{0xC7, 0x06}, i32(6)...)
	return append(append(out, 0xC7, 0x07), i32(n)...)
}

/*
CapDropDenom rewrites the chanceDenominator load feeding a drop roll so the
denominator is clamped.

A drop rolls below chanceDenominator and succeeds when the roll is below
chanceNumerator, so a *smaller* denominator only ever raises the chance. A
percentage of 100 gives a cap of 1, the roll is always 0, and the drop is
guaranteed; 50 gives a cap of 2, which is a one-in-two floor. An item that
already drops more often than that is untouched, because this is a minimum and
never a maximum.

It replaces both displaced instructions rather than running before them, which is
why the injection that uses it does not re-run what it overwrote: it reproduces
the store itself.

	mov ecx,[esi+10]   the chanceDenominator
	cmp ecx, cap
	jle +5             already at or below the cap, keep it
	mov ecx, cap
	mov [esp+04],ecx   the reproduced store
*/
func CapDropDenom(pct int32) []byte {
	// The percentage comes from a value whose range starts at 1. Dividing by it
	// anyway is guarded here rather than left to fault: the Python raises on a
	// zero, which is a crash report from a caller that was already out of range,
	// and the useful behaviour is to treat it as the smallest percentage there
	// is.
	floor := max32(int32(100)/max32(pct, 1), 1)

	out := []byte{0x8B, 0x4E, 0x10}
	out = append(out, 0x81, 0xF9)
	out = append(out, i32(floor)...)
	out = append(out, 0x7E, 0x05)
	out = append(out, 0xB9)
	out = append(out, i32(floor)...)
	return append(out, 0x89, 0x4C, 0x24, 0x04)
}

// max32 is the larger of two words.
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
