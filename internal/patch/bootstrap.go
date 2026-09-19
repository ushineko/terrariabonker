package patch

import (
	"bytes"
	"fmt"
	"time"
)

/*
Springboards are where the arena bootstrap may hang its temporary hook:
an anchor, an offset into it, and the bytes expected there.

Each has to be a per-frame site whose bytes carry no relative address, so they
can be replayed from the temporary stub.

The first is deliberately *not* any injection's hook site. It sits inside the
borders_movement anchor but past auto-use's displaced bytes, at the instruction
auto-use's own stub jumps back to, so it runs every frame whether that cheat is
on or off. The second is kept as a fallback for a build where the first anchor
does not resolve.
*/
var Springboards = []struct {
	Anchor string
	Off    int
	Expect []byte
}{
	{"borders_movement", 0x12, []byte{0x8B, 0x45, 0x08, 0xD9, 0xEE}},
	{"grabitems_call", 0x15, []byte{0x89, 0x04, 0x24, 0x8B, 0xC0}},
}

/*
springboard is a per-frame site to hang the bootstrap on, and the bytes it
displaces.

Candidates are tried in order and one whose bytes are not what is expected is
*skipped*, not forced: the commonest reason is that a cheat is already hooked
there. The bootstrap used to use the extractor's own injection site, so a player
with the ore extractor enabled and no arena to adopt could not allocate one at
all -- latent until the arena stamp changed and forced a fresh allocation into a
session whose cheats had been restored on launch.
*/
func (p *Patcher) springboard() (uint32, []byte, error) {
	var tried []string
	for _, sb := range Springboards {
		res := p.Scanner.Resolve(sb.Anchor, "")
		if !res.Available {
			tried = append(tried, fmt.Sprintf("%s: %s", sb.Anchor, res.Reason))
			continue
		}
		site := offsetBy(res.Sites[0], sb.Off)
		got := p.Mem.Read(site, len(sb.Expect))
		if bytes.Equal(got, sb.Expect) {
			return site, sb.Expect, nil
		}
		tried = append(tried, fmt.Sprintf("%s+%#x: bytes are % X, expected % X "+
			"(something is hooked there)", sb.Anchor, sb.Off, got, sb.Expect))
	}
	return 0, nil, fmt.Errorf("no free springboard site for the arena bootstrap: %v", tried)
}

/*
bootstrapArena makes the game allocate memory for this program, then takes the
hook back off.

One process cannot allocate into another, so the game allocates for itself: a
small springboard in a code cave calls VirtualAlloc at a *fixed* base. Fixed,
because a stub in a read-execute cave has nowhere to report a return value to --
so rather than read the result, the address is chosen and then looked for in the
process map.

The springboard hangs on a per-frame site, so it fires within a frame.

**The unhook happens whatever else does**: a springboard left hooked is a jump
into bytes this scrubs on its way out, which is a crash on the next frame rather
than a failed allocation.

Whether the memory appeared is the caller's question, answered by looking at the
map.
*/
func (p *Patcher) bootstrapArena(base uint32, timeout time.Duration) error {
	va, err := ResolveExport(p.Mem, "kernel32", "VirtualAlloc")
	if err != nil {
		return err
	}
	site, overwrite, err := p.springboard()
	if err != nil {
		return err
	}

	body := []byte{0x60} // pushad
	body = append(body, 0x6A, 0x40)
	body = append(body, 0x68)
	body = append(body, u32(0x3000)...) // commit and reserve
	body = append(body, 0x68)
	body = append(body, u32(ArenaSize)...)
	body = append(body, 0x68)
	body = append(body, u32(base)...) // the fixed address
	body = append(body, 0xB8)
	body = append(body, u32(va)...)
	body = append(body, 0xFF, 0xD0) // call it; the callee cleans up
	body = append(body, 0x61)       // popad
	body = append(body, overwrite...)

	stubLen := len(body) + 5
	cave, err := FindCave(p.Scanner, stubLen, nil, false, p.state)
	if err != nil {
		return err
	}
	if err := CheckSite(p.Mem, site, overwrite, "arena bootstrap"); err != nil {
		return err
	}

	stub := append(append([]byte{}, body...), 0xE9)
	stub = append(stub, Rel32(cave+uint32(len(body)), site+5)...) //nolint:gosec // a short stub
	p.Mem.Write(cave, stub)
	p.Mem.Write(site, append([]byte{0xE9}, Rel32(site+5, cave)...))

	if p.OnWait != nil {
		p.OnWait()
	}
	// Unhooked first and always, whatever happened above.
	defer func() {
		p.Mem.Write(site, overwrite)
		p.Mem.Write(cave, bytes.Repeat([]byte{0xCC}, stubLen))
	}()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := Mapped(p.Mem, base); ok {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
