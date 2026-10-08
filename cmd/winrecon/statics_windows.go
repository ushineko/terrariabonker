//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
)

// Main's static offsets as cmd/clrfields reported them (spec 052).
const (
	clrMainTile       = 0x810
	clrMainNPC        = 0x824
	clrMainProjectile = 0x830
	clrMainPlayer     = 0x878
	clrMainMaxTilesX  = 0x1228
	clrMainMyPlayer   = 0x1384
	clrMainGameMenu   = 0x1656
)

// words is every 4-aligned address in writable memory whose dword is v.
func words(g game, data []region, match func(buf []byte, i int) bool) []uint32 {
	var out []uint32
	const chunk = 1 << 20
	for _, r := range data {
		for base := r.start; base < r.end; base += chunk {
			size := chunk
			if rem := int(r.end - base); rem < size {
				size = rem
			}
			buf := g.read(base, size+8)
			for i := 0; i+8 <= len(buf) && i < size; i += 4 {
				if match(buf, i) {
					out = append(out, base+uint32(i))
				}
			}
		}
	}
	return out
}

// arrayAt reports the szarray an element slot belongs to, by walking back to
// a header whose length covers the slot: MethodTable, length, data at +8.
func arrayAt(g game, slot uint32) (uint32, uint32, bool) {
	for back := uint32(0); back <= 300*4; back += 4 {
		start := slot - 8 - back
		n, ok := g.u32(start + 4)
		if ok && n > 0 && n <= 0x10000 && back/4 < n {
			if mt, _ := g.u32(start); mt > 0x10000 {
				return start, n, true
			}
		}
	}
	return 0, 0, false
}

func clrStatics(g game, data []region, player uint32) {
	fmt.Printf("\n== Main statics (player object %08x) ==\n", player)
	refs := words(g, data, func(b []byte, i int) bool { return binary.LittleEndian.Uint32(b[i:]) == player })
	for _, slot := range refs {
		arr, n, ok := arrayAt(g, slot)
		if !ok || n < 200 {
			continue
		}
		fmt.Printf("  slot %08x in array %08x len %d (index %d)\n", slot, arr, n, (slot-arr-8)/4)
		holders := words(g, data, func(b []byte, i int) bool { return binary.LittleEndian.Uint32(b[i:]) == arr })
		for _, h := range holders {
			base := h - clrMainPlayer
			npc, _ := g.u32(base + clrMainNPC)
			npcLen, _ := g.u32(npc + 4)
			proj, _ := g.u32(base + clrMainProjectile)
			projLen, _ := g.u32(proj + 4)
			tile, _ := g.u32(base + clrMainTile)
			fmt.Printf("    held at %08x -> ref base %08x: npc[] len %d, projectile[] len %d, tile %08x\n",
				h, base, npcLen, projLen, tile)
		}
	}
	for _, dims := range [][2]uint32{{4200, 1200}, {6400, 1800}, {8400, 2400}} {
		hits := words(g, data, func(b []byte, i int) bool {
			return binary.LittleEndian.Uint32(b[i:]) == dims[0] && binary.LittleEndian.Uint32(b[i+4:]) == dims[1]
		})
		for _, a := range hits {
			base := a - clrMainMaxTilesX
			my, _ := g.u32(base + clrMainMyPlayer)
			menu := g.read(base+clrMainGameMenu, 1)
			fmt.Printf("  %dx%d at %08x -> prim base %08x: myPlayer=%d gameMenu=%v\n", dims[0], dims[1], a, base, my, menu)
		}
	}
}
