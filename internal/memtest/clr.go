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

/*
Main's reference statics and the arrays they hold, as the CLR keeps them
(spec 052 phase 0, measured on the live game).

An szarray is a MethodTable, a length, then its elements from +8. The Main.player
slot holds a Player[256]; Main.npc is 0x54 below it and Main.projectile 0x48 below
it (cmd/clrfields: 0x824 and 0x830 against 0x878), holding an NPC[201] and a
Projectile[1001]. A player object keeps Player.active, a bool, at +0x70E and
statLife at +0x470.
*/
const (
	CLRArrayMT           = 0xA77A7000
	CLRNPCFromPlayer     = -0x54
	CLRProjFromPlayer    = -0x48
	CLRPlayerActive      = 0x70E
	CLRLifeInPlayer      = 0x470
	CLRPlayerSlots       = 256
	CLRNPCSlots          = 201
	CLRProjectileSlots   = 1001
	clrArrayHeader       = 8
	clrArrayLengthOffset = 4
)

// PlantCLRArray writes an szarray header at addr and its elements after it.
func (m *FakeMem) PlantCLRArray(addr uint32, length int, elems []uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], CLRArrayMT)
	m.PokeBytes(addr, b[:])
	binary.LittleEndian.PutUint32(b[:], count(length))
	m.PokeBytes(addr+clrArrayLengthOffset, b[:])
	for i, e := range elems {
		binary.LittleEndian.PutUint32(b[:], e)
		m.PokeBytes(addr+clrArrayHeader+uint32(4*i), b[:]) //nolint:gosec // an element index
	}
}

// PlantCLRActive sets a player object's Player.active.
func (m *FakeMem) PlantCLRActive(obj uint32, active bool) {
	v := byte(0)
	if active {
		v = 1
	}
	m.PokeBytes(obj+CLRPlayerActive, []byte{v})
}

// PlantCLRStatics writes Main.player's slot and its two neighbours, pointing at
// the three arrays.
func (m *FakeMem) PlantCLRStatics(slot, players, npcs, projectiles uint32) {
	var b [4]byte
	for at, arr := range map[int]uint32{0: players, CLRNPCFromPlayer: npcs, CLRProjFromPlayer: projectiles} {
		binary.LittleEndian.PutUint32(b[:], arr)
		m.PokeBytes(shift(slot, at), b[:])
	}
}

/*
CLRWorld is a game in a world, the CLR way: the live player in Main.player[0],
active; a load-time snapshot of the same character outside the array, which a scan
finds too; inactive placeholders in every other slot; Main's statics pointing at
the three arrays.

The snapshot sits at the lower address, so a scan finds it first: a reader that
took the first copy would pick the wrong one.
*/
type CLRWorld struct {
	Mem                              *FakeMem
	Live, Snap, Placeholder          uint32 // player objects
	Players, NPCs, Projectiles, Slot uint32
	Base                             uint32
}

// CLRWorldSize is how much memory PlantCLRWorld needs from base.
const CLRWorldSize = 0x20000

// PlantCLRWorld plants a CLRWorld at base in a fresh fake.
func PlantCLRWorld(base uint32) CLRWorld {
	w := CLRWorld{
		Mem: New(base, CLRWorldSize), Base: base,
		Snap: base + 0x1000, Live: base + 0x3000, Placeholder: base + 0x5000,
		Players: base + 0x8000, NPCs: base + 0x9000, Projectiles: base + 0xA000,
		Slot: base + 0x10878,
	}
	w.Mem.PlantCLRString(base+0x40, "terrariabonker")
	for _, obj := range []uint32{w.Snap, w.Live} {
		w.Mem.PlantCLRPlayer(obj+CLRLifeInPlayer, []int32{400, 400, 380, 200, 200, 200}, base+0x40)
		w.Mem.PlantCLRActive(obj, true)
	}
	w.Mem.PlantCLRActive(w.Placeholder, false)
	w.Mem.PlantCLRArray(w.Players, CLRPlayerSlots, w.Elements(w.Live))
	w.Mem.PlantCLRArray(w.NPCs, CLRNPCSlots, nil)
	w.Mem.PlantCLRArray(w.Projectiles, CLRProjectileSlots, nil)
	w.Mem.PlantCLRStatics(w.Slot, w.Players, w.NPCs, w.Projectiles)
	return w
}

// Elements is a Main.player array's slots with live in slot 0 and placeholders
// everywhere else.
func (w CLRWorld) Elements(live uint32) []uint32 {
	elems := make([]uint32, CLRPlayerSlots)
	for i := range elems {
		elems[i] = w.Placeholder
	}
	elems[0] = live
	return elems
}
