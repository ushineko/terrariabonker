package patch

import (
	"encoding/binary"
	"fmt"

	"github.com/ushineko/terrariabonker/internal/locate"
)

/*
The stubs that are built from what is in the game rather than from a value.

Each of these bakes addresses that are only known once the game is running --
resolved method entries, the statics that lead to the player, this program's own
arena -- so they cannot be constants. What they assemble to is fixed, and that is
what the tests compare.
*/

/*
The arena's reserved region, which holds data rather than code.

The extractor's queue is a count followed by that many coordinate pairs. Thirty
two per swing is what turns "one block per swing" into "the vein just goes" -- a
typical vein is ten to thirty tiles -- while draining a four-hundred-tile vein in
one call would run four hundred PickTiles in a single frame, each spawning dust,
drops and light updates, and would visibly hitch.

The auto-use words sit clear of that queue. The first version of them overlapped
it: mining a vein wrote the extractor's tile count into the arm word, and the
stub pressed the use button for every batch queued. Nothing in the auto-use code
was involved, which is what made it baffling in the log.
*/
const (
	OreMaxBatch  = 32
	OreQueueOff  = 0x400 // a count, then OreMaxBatch pairs: 0x400..0x504
	OreQueueSize = 4 + OreMaxBatch*8

	AutoUseArmedOff   = 0x600 // set by the trainer, cleared by the stub
	AutoUseCountOff   = 0x604 // the stub's own tally of presses made
	AutoUseReleaseOff = 0x608 // which second byte the stub sets, or 0 for none
)

/*
OrePickPower is the pick power handed to PickTile.

A tile breaks at 100 accumulated damage, and the game credits harder tiles at a
fraction of the power given -- so this is not "how strong", it is "enough that one
hit still clears 100 after the worst division". The stub queues each tile once
and the count is consumed whether or not the tile broke, so a tile that survives
its hit is never hit again: in the game that is the block poofing, staying put,
and still needing mining by hand.

From the game's own code, for what the whitelist can queue: halved for cobalt,
palladium, hellstone, ebonstone, crimstone and dungeon brick; a third for mythril
and orichalcum; a quarter for adamantite, titanium and lihzahrd brick; a fifth
for chlorophyte, which also takes no damage at all below power 200.

100 broke nothing that was halved. 250 cleared the halved tiles and was reported
as fixed, which it was for hellstone -- and left every hardmode ore from
orichalcum up silently unmineable, because 250 over 3 is 83. The floor is 500,
for chlorophyte; 1000 is that with headroom, and nothing scales with the value
beyond the one tile being hit.
*/
const OrePickPower = 1000

/*
UseItemOff is Player.controlUseItem, from the Player object base.

ReleaseItemOff is Player.releaseUseItem. Setting the control alone reels a bobber
in but never casts: the bobber pull runs off controlUseItem, while *starting* a
use needs a fresh press -- both flags -- for an item that is not auto-reuse. Found
by hammering controlUseItem and taking the byte that inverts: all ones at idle,
two per cent while using, thirteen bytes along from the control it mirrors.
*/
const (
	UseItemOff     = 0x672
	ReleaseItemOff = 0x67F
)

/*
f32Times16 multiplies a float by sixteen by adding four to its exponent field.

Valid for the normalised positive numbers this is used on, and it needs no
floating-point instruction and no memory constant.
*/
const f32Times16 = 0x02000000

/*
CallTarget is the entry point of the method a call instruction inside an anchor
match invokes.

Cheaper and steadier than anchoring each callee's own prologue: these call sites
are already anchored and verified for other cheats.
*/
func CallTarget(b *Builder, anchorKey string, callOff int) (uint32, error) {
	res := b.Scanner.Resolve(anchorKey, "")
	if !res.Available {
		return 0, fmt.Errorf("%s", res.Reason)
	}
	site := res.Sites[0] + uint32(callOff) //nolint:gosec // an offset inside a match
	rel := b.Mem.Read(site+1, 4)
	if len(rel) < 4 {
		return 0, fmt.Errorf("the call at %#x is not readable", site)
	}
	return site + 5 + binary.LittleEndian.Uint32(rel), nil
}

/*
TeleportBody is the managed-call stub for the map-ping teleport.

It is injected where the ping's position is still on the stack, calls
Player.Teleport with it, and then reproduces the two displaced instructions so
the ping itself still happens.

TriggerPing delivers the ping in *tile* coordinates, while Teleport sets
Player.position, which is in world pixels at sixteen to the tile. The stub
converts by adding to each coordinate's exponent, verified against live data:
tile 3501.84 became 56029.4 pixels, landing on the pinged spot.

The 32-bit runtime passes every argument on the stack, right to left. Rather than
assume whether the caller or the callee cleans them up -- mono emits both -- esp
is saved before the pushes and restored after the call through ebx, which
Teleport preserves, so the stub is correct either way. Style 0 avoids Teleport's
special branches, and the register save protects what the original code still
needs: eax is loaded upstream and consumed as the ping-list argument just after
the injection site.
*/
func TeleportBody(playerBase, callTarget uint32) []byte {
	out := []byte{0x60} // pushad
	out = append(out, 0x8B, 0xDC)
	out = append(out, 0x8B, 0x45, 0x08) // mov eax,[ebp+08] -- the ping's tile X
	out = append(out, 0x05)
	out = append(out, i32(f32Times16)...)
	out = append(out, 0x8B, 0x4D, 0x0C) // mov ecx,[ebp+0C] -- the tile Y
	out = append(out, 0x81, 0xC1)
	out = append(out, i32(f32Times16)...)
	out = append(out, 0x6A, 0x00) // extraInfo
	out = append(out, 0x6A, 0x00) // Style
	out = append(out, 0x51)       // the pixel Y
	out = append(out, 0x50)       // the pixel X
	out = append(out, 0x68)       // this
	out = append(out, u32(playerBase)...)
	out = append(out, 0xB8)
	out = append(out, u32(callTarget)...)
	out = append(out, 0xFF, 0xD0) // call eax
	out = append(out, 0x8B, 0xE3) // mov esp,ebx -- correct either convention
	out = append(out, 0x61)       // popad
	// The two displaced instructions, reproduced.
	out = append(out, 0x8B, 0x4D, 0x08)
	return append(out, 0x89, 0x4C, 0x24, 0x04)
}

/*
InventoryAccsBody makes accessories work from the inventory.

It is injected in the loop that already walks the inventory every frame -- vanilla
uses it to refresh info and mechanical accessories, which is why a Depth Meter
works from the bag today -- at the point where the Item pointer is in eax. It
calls the three methods an equipped accessory goes through: the effects, the
modifier benefits and the per-item extras.

The accessory flag is tested first. The loop runs 58 times a frame and the
effects method is 11.6 KB, so calling it for every stack of dirt would be pure
waste; typically a handful of items pass.

Slot 0 is passed because the slot argument is used for one thing only, an index
into an array of ten booleans.

Register discipline follows the teleport stub: everything saved and restored
around the calls, the Item pointer parked in a register the callee preserves, and
esp restored after each call so the stub is correct whichever way the arguments
were cleaned up.
*/
func InventoryAccsBody(b *Builder) ([]byte, error) {
	applyFn, err := CallTarget(b, "equip_apply", 15) // the effects
	if err != nil {
		return nil, err
	}
	prefixFn, err := CallTarget(b, "equip_benefits", 20) // the modifier benefits
	if err != nil {
		return nil, err
	}
	armorFn, err := CallTarget(b, "equip_benefits", 36) // the per-item extras
	if err != nil {
		return nil, err
	}

	// call is `mov eax,<entry>; call eax; mov esp,ebx`.
	call := func(target uint32) []byte {
		out := append([]byte{0xB8}, u32(target)...)
		return append(out, 0xFF, 0xD0, 0x8B, 0xE3)
	}

	guarded := []byte{0x60}               // pushad
	guarded = append(guarded, 0x8B, 0xF0) // mov esi,eax -- the Item pointer
	guarded = append(guarded, 0x8B, 0xDC) // mov ebx,esp
	guarded = append(guarded, 0x56, 0x6A, 0x00, 0x57)
	guarded = append(guarded, call(applyFn)...)
	guarded = append(guarded, 0x56, 0x57)
	guarded = append(guarded, call(prefixFn)...)
	guarded = append(guarded, 0x56, 0x57)
	guarded = append(guarded, call(armorFn)...)
	guarded = append(guarded, 0x61) // popad -- eax is the Item pointer again

	if len(guarded) > 127 {
		return nil, fmt.Errorf("the accessory stub grew to %d bytes -- too far to "+
			"jump over in one byte", len(guarded))
	}

	out := []byte{0x8B, 0x00}                   // the displaced load: the Item pointer
	out = append(out, 0x80, 0x78, 0x7D, 0x00)   // cmp byte [eax+7D],0 -- is it an accessory
	out = append(out, 0x74, byte(len(guarded))) //nolint:gosec // checked above
	out = append(out, guarded...)
	return append(out, 0x8B, 0x40, 0x6C), nil // the displaced load of the item type
}

/*
OreExtractBody mines a batch of queued tiles every frame.

The unprivileged side does the thinking -- read the tile map, flood-fill the vein,
decide what may be taken -- and writes a queue here. This walks it and calls
PickTile for each, the same call the game makes on a swing, so drops, framing,
lighting and the can-mine check all happen exactly as they normally would.

**Why the per-frame site and not PickTile.** Hooking PickTile itself only ran the
stub when the player swung, and the queue is armed *after* a swing has broken a
tile -- so a vein sat armed until the player happened to swing again, and breaking
one block and stopping did nothing at all. Hooking a per-frame call means an
armed queue drains on the next frame, which is what "break one block and the vein
goes" actually requires. It also removes re-entrancy completely: PickTile is no
longer hooked, so calling it cannot come back through here.

**The count is consumed before the work.** Reading it and zeroing it immediately
means a batch is mined once rather than re-mined every frame at sixty a second,
and the queue cannot be drained twice if anything ever does re-enter.

**The count is clamped in the stub as well as by the caller**, because a corrupted
count would not crash: it would mine coordinates nobody asked for, which damages
a world.

**Stack alignment.** Mono's x86 JIT builds its frames assuming a particular
alignment at entry, and PickTile's own prologue proves it. A register holds the
aligned base and each iteration restores esp from it, so every call in the batch
gets the alignment mono expects however PickTile cleans up.
*/
func OreExtractBody(b *Builder, overwrite []byte) ([]byte, error) {
	res := b.Scanner.Resolve("pick_tile", "") // the call target, not the hook site
	if !res.Available {
		return nil, fmt.Errorf("%s", res.Reason)
	}
	pickTile := res.Sites[0]

	anchor, ok := locate.FindLocalPlayerAnchor(b.Mem)
	if !ok {
		return nil, fmt.Errorf("could not locate Main.player / Main.myPlayer")
	}
	playerArr, okArr := readWord(b.Mem, anchor-0xA)
	myPlayer, okMy := readWord(b.Mem, anchor-4)
	if !okArr || !okMy || playerArr == 0 || myPlayer == 0 {
		return nil, fmt.Errorf("cannot read Main.player / Main.myPlayer")
	}
	queue := b.Arena + OreQueueOff

	body := []byte{0x8B, 0xE5} // mov esp,ebp -- realigned each iteration
	body = append(body, 0xA1)
	body = append(body, u32(playerArr)...)
	body = append(body, 0x8B, 0x0D)
	body = append(body, u32(myPlayer)...)
	body = append(body, 0x8B, 0x44, 0x88, 0x10) // the local player
	body = append(body, 0x6A, 0xFF)             // the cap, as the game's own callers pass
	body = append(body, 0x68)
	body = append(body, u32(OrePickPower)...)
	body = append(body, 0xFF, 0x76, 0x04) // the queued y
	body = append(body, 0xFF, 0x36)       // the queued x
	body = append(body, 0x50)             // this
	body = append(body, 0xB8)
	body = append(body, u32(pickTile)...)
	body = append(body, 0xFF, 0xD0)
	body = append(body, 0x83, 0xC6, 0x08) // on to the next pair
	body = append(body, 0x4F)             // dec the counter

	if len(body)+2 > 128 {
		return nil, fmt.Errorf("the extractor loop grew to %d bytes -- too far to "+
			"jump back in one byte", len(body))
	}
	body = append(body, 0x75, byte(256-(len(body)+2))) //nolint:gosec // checked above
	body = append(body, 0x8B, 0xE3)                    // restore esp, either convention
	tail := body

	setup := []byte{0x83, 0xFF, OreMaxBatch} // cmp the count against the cap
	setup = append(setup, 0x76, 0x05)
	setup = append(setup, 0xBF)
	setup = append(setup, u32(OreMaxBatch)...) // clamp a bad count
	setup = append(setup, 0xBE)
	setup = append(setup, u32(queue+4)...) // the first pair
	setup = append(setup, 0x8B, 0xDC)
	setup = append(setup, 0x83, 0xE4, 0xF0) // align, as mono expects at entry
	setup = append(setup, 0x83, 0xEC, 0x0C)
	setup = append(setup, 0x8B, 0xEC) // the aligned base

	skip := 7 + len(setup) + len(tail)
	if skip >= 128 {
		return nil, fmt.Errorf("the extractor stub grew to %d bytes -- too far to "+
			"jump over in one byte", skip)
	}
	empty := []byte{0x8B, 0x3D}
	empty = append(empty, u32(queue)...) // the count
	empty = append(empty, 0x85, 0xFF)
	empty = append(empty, 0x74, byte(skip)) //nolint:gosec // checked above
	empty = append(empty, 0x83, 0x25)
	empty = append(empty, u32(queue)...)
	empty = append(empty, 0x00) // consume it before doing the work

	out := []byte{0x60} // pushad
	out = append(out, empty...)
	out = append(out, setup...)
	out = append(out, tail...)
	out = append(out, 0x61) // popad -- where the skip lands
	return append(out, overwrite...), nil
}

/*
AutoUseBody presses the use button once, on the next frame, when the trainer arms
it.

The poller this replaces won by volume, around four hundred thousand writes a
second, covering every frame many times over. That takes a fish, but it cannot
promise *one* of anything: a twenty-millisecond burst took the water from one
bobber to three, catching the fish and re-casting twice. A stub that runs once
per frame is correct by construction instead.

**Consume before acting.** The flag is cleared before the byte is written, the
same ordering the extractor uses: a stub that dies between the two presses
nothing, where the other order would press forever.

**`this` is free here.** The site's first displaced instruction loads
Player.Update's own argument, so the player pointer costs nothing and cannot be
stale.

**No calls, so no calling convention.** Every crash in the reach work came from a
call: argument order, frame alignment, or the cave the stub lived in. This one
calls nothing, touches no stack beyond its own saves, and writes two words of
this program's arena.

The counter exists for the first test: arm it N times and read N back, without
any claim about what the game did with the presses.

The two words this stub reads are *initialised* by the enable path rather than
here. The Python writes them while building the body, which makes building a stub
modify the game; keeping that out means the bytes can be built and compared
without touching anything.
*/
func AutoUseBody(arena uint32, overwrite []byte) []byte {
	armed := arena + AutoUseArmedOff
	count := arena + AutoUseCountOff
	release := arena + AutoUseReleaseOff

	tail := []byte{0xC6, 0x80}
	tail = append(tail, u32(UseItemOff)...)
	tail = append(tail, 0x01)
	// And the release flag, so the game reads a fresh press rather than a hold.
	// Its offset lives in the arena rather than in the instruction, so a
	// candidate can be tried without re-patching; zero means "the control only".
	tail = append(tail, 0x8B, 0x0D)
	tail = append(tail, u32(release)...)
	tail = append(tail, 0x85, 0xC9)
	tail = append(tail, 0x74, 0x04)
	tail = append(tail, 0xC6, 0x04, 0x08, 0x01)
	tail = append(tail, 0xFF, 0x05)
	tail = append(tail, u32(count)...)

	press := []byte{0x83, 0x25}
	press = append(press, u32(armed)...)
	press = append(press, 0x00)             // consume the flag first
	press = append(press, 0x8B, 0x45, 0x08) // this, from Update's own argument
	press = append(press, 0x85, 0xC0)
	press = append(press, 0x74, byte(len(tail))) //nolint:gosec // a fixed, short body
	press = append(press, tail...)

	out := []byte{0x60, 0x9C} // pushad; pushfd
	out = append(out, 0x83, 0x3D)
	out = append(out, u32(armed)...)
	out = append(out, 0x00)
	out = append(out, 0x74, byte(len(press))) //nolint:gosec // a fixed, short body
	out = append(out, press...)
	out = append(out, 0x9D, 0x61) // popfd; popad -- where the skip lands
	return append(out, overwrite...)
}

// readWord is one little-endian word, and whether it was readable.
func readWord(mem Mem, addr uint32) (uint32, bool) {
	b := mem.Read(addr, 4)
	if len(b) < 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}
