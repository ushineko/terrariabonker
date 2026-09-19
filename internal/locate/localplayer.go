package locate

import (
	"bytes"
	"encoding/binary"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
Resolving Main.player[Main.myPlayer] -- the authoritative live player.

FindPlayers returns every copy that looks like a player, which is usually the
live one plus an inert load-time snapshot or two. Telling them apart by watching
which one changes needs the game to be running frames, and it usually is not:
Terraria pauses in single-player whenever its window loses focus, which is what
happens the moment anyone clicks in the trainer.

So the live one is resolved rather than guessed. Main.get_LocalPlayer is
`return Main.player[Main.myPlayer]`, and its JIT'd tail is a distinctive shape --
bounds check, index, load, return. Finding that tail gives the addresses of the
two statics the instructions before it read, and those lead to the player the
game itself would use.

Ported from terrariabonker/locate.py (spec 051, step 3).
*/

/*
localPlayerTail is the JIT'd tail of Main.get_LocalPlayer:

	cmp  [eax+0C],ecx     ; bounds check against the array length
	jbe  +7               ; out of range, fall through to the throw
	lea  eax,[eax+ecx*4+10]
	mov  eax,[eax]
	ret

The index-and-return shape is what makes it unique. The two `mov reg,[abs]`
before it load Main.player and Main.myPlayer, which is what this is really
after.
*/
var localPlayerTail = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}

// LocalPlayerTail is a copy of that, so a test can check it against its own
// without being able to change it.
func LocalPlayerTail() []byte { return append([]byte{}, localPlayerTail...) }

// Where the two operands sit behind the tail, and the shape of the two
// instructions that carry them.
const (
	playerStaticBack   = 0xA // mov eax,[Main.player]
	myPlayerStaticBack = 0x4 // mov ecx,[Main.myPlayer]
	preambleBack       = 0xC
	// StatLifeFromObj is Player.statLife within the Player object, which is how
	// an object address becomes the address a block is read from.
	StatLifeFromObj = 0x738
	// maxPlayers bounds the index read from the game: a myPlayer outside it
	// means the anchor is stale rather than that there are that many players.
	maxPlayers = 256
)

// ExecMem is memory whose executable regions can be listed. The pattern lives
// in JIT'd code, which the writable-region scan deliberately does not cover.
type ExecMem interface {
	Mem
	// ExecRegions is the executable, non-device memory.
	ExecRegions() []proc.Region
}

/*
FindLocalPlayerAnchor is the address of the get_LocalPlayer tail, or reports
that it is not there.

The expensive half of resolving the live player, split out so a long-lived
caller finds it once and keeps it. A pattern that appears more than once is
treated as not found: two candidates mean the shape is no longer unique, and
guessing between them would resolve the wrong array.
*/
func FindLocalPlayerAnchor(mem ExecMem) (uint32, bool) {
	var anchor uint32
	var found bool
	for _, region := range mem.ExecRegions() {
		buf := mem.Read(region.Start, region.Size())
		for i := 0; ; {
			at := bytes.Index(buf[i:], localPlayerTail)
			if at < 0 {
				break
			}
			at += i
			if precededByStaticLoads(buf, at) {
				if found {
					return 0, false // not unique: the caller falls back to the scan
				}
				anchor, found = region.Start+uint32(at), true //nolint:gosec // an offset in a 32-bit region
			}
			i = at + 1
		}
	}
	return anchor, found
}

// precededByStaticLoads reports whether the two `mov reg,[abs]` that load
// Main.player and Main.myPlayer sit where they should, which is what tells this
// tail from another array's.
func precededByStaticLoads(buf []byte, at int) bool {
	if at < preambleBack {
		return false
	}
	return buf[at-0xC] == 0x8B && buf[at-0xB] == 0x05 && // mov eax,[abs]
		buf[at-6] == 0x8B && buf[at-5] == 0x0D // mov ecx,[abs]
}

/*
MainStaticBase is the base of Terraria.Main's static data block.

Main.player's static address minus its own offset, so any Main static is
reachable by adding the offset of the field wanted.
*/
func MainStaticBase(mem ExecMem) (uint32, bool) {
	anchor, ok := FindLocalPlayerAnchor(mem)
	if !ok {
		return 0, false
	}
	playerStatic, ok := readU32(mem, anchor-playerStaticBack)
	if !ok {
		return 0, false
	}
	return playerStatic - layout.MainPlayerOff, true
}

/*
LocalPlayerAt reads Main.player[Main.myPlayer] through an anchor already found.

The cheap half: four pointer reads and no scanning. It re-reads the statics
every time, so it corrects itself when the managed heap moves the Player object
-- a cached anchor stays usable across a garbage collection. A failure here is
the caller's signal that the anchor has gone and has to be found again.
*/
func LocalPlayerAt(mem Mem, anchor uint32) (Block, bool) {
	playerStatic, ok := readU32(mem, anchor-playerStaticBack)
	if !ok || playerStatic == 0 {
		return Block{}, false
	}
	myPlayerStatic, ok := readU32(mem, anchor-myPlayerStaticBack)
	if !ok || myPlayerStatic == 0 {
		return Block{}, false
	}
	array, ok := readU32(mem, playerStatic)
	if !ok || array == 0 {
		return Block{}, false
	}
	index, ok := readU32(mem, myPlayerStatic)
	if !ok || index >= maxPlayers {
		return Block{}, false
	}
	object, ok := readU32(mem, array+index*4+layout.ArrDataOff)
	if !ok || object == 0 {
		return Block{}, false
	}
	return ReadBlock(mem, object+StatLifeFromObj)
}

/*
ResolveLocalPlayer is the live player, found from scratch.

Ground truth, and it works while the game is paused -- which it usually is,
because the trainer having focus is what pauses it. Reports failure when the
pattern is missing, which is what a game update looks like, and the caller falls
back to scanning and guessing.
*/
func ResolveLocalPlayer(mem ExecMem) (Block, bool) {
	anchor, ok := FindLocalPlayerAnchor(mem)
	if !ok {
		return Block{}, false
	}
	return LocalPlayerAt(mem, anchor)
}

// readU32 is one little-endian word, and whether it was readable.
func readU32(mem Mem, addr uint32) (uint32, bool) {
	b := mem.Read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}
