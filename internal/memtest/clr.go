package memtest

import (
	"encoding/binary"
	"unicode/utf16"
)

/*
CLR object layout, as .NET Framework 4.8.1 keeps it (spec 052 phase 0).

Written out here as literals from the measurement, not read from internal/layout:
a fixture planted through layout's numbers and read back through them proves only
that the numbers equal themselves.

A 32-bit CLR String is a MethodTable pointer, a length in characters, then UTF-16
-- four bytes shorter than mono's, which has a sync block word before the length.
On the live game, cmd/winrecon decoded all seven characters' names this way.

The life and mana block starts two words before statLife, as under mono, but its
first two words are the other way round: statLifeMax, then statLifeMax2
(cmd/clrfields: 0x468 and 0x46C, statLife 0x470). The name pointer is 0x3E4 below
statLife (Player.name at 0x08C).
*/
const (
	CLRStringMT         = 0xC1A55E50
	CLRStringHeader     = 8 // MethodTable, length
	CLRPlayerNameOffset = -0x3E4
)

// PlantCLRString writes a CLR String at addr: the header, then the text as
// UTF-16.
func (m *FakeMem) PlantCLRString(addr uint32, text string) {
	units := utf16.Encode([]rune(text))
	out := make([]byte, CLRStringHeader, CLRStringHeader+len(units)*2)
	binary.LittleEndian.PutUint32(out[0:], CLRStringMT)
	binary.LittleEndian.PutUint32(out[4:], count(len(units)))
	for _, unit := range units {
		out = binary.LittleEndian.AppendUint16(out, unit)
	}
	m.PokeBytes(addr, out)
}

/*
PlantCLRPlayer writes a life/mana block as the CLR stores it and the name pointer
that goes with it.

stored is in storage order: statLifeMax, statLifeMax2, statLife, statMana,
statManaMax, statManaMax2.
*/
func (m *FakeMem) PlantCLRPlayer(lifeAddr uint32, stored []int32, namePtr uint32) {
	for i, value := range stored {
		m.PokeI32(shift(lifeAddr, PlayerBlockOffset+i*4), value)
	}
	var ptr [4]byte
	binary.LittleEndian.PutUint32(ptr[:], namePtr)
	m.Write(shift(lifeAddr, CLRPlayerNameOffset), ptr[:])
}
