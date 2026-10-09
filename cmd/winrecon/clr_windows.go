//go:build windows

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
)

/*
Offsets cmd/clrfields reported for Terraria.Player on .NET Framework 4.8.1,
object-relative (the 4-byte MethodTable pointer included). Measured
2026-10-08 against 1.4.5.8+24893155; the FieldDesc list agreed with the
metadata's static flag on 1195 of 1195 records.
*/
const (
	clrStatLifeMax   = 0x468
	clrStatLife      = 0x470
	clrName          = 0x08C
	clrInventory     = 0x0D4
	clrInventoryHint = clrInventory - clrStatLife
)

var playerMT = flag.Uint("mt", 0, "Player MethodTable from clrfields, to check the object header")

// clrPlayer scans for the life/mana block in the CLR's field order
// (statLifeMax before statLifeMax2) and checks each hit against the measured
// name and inventory offsets and, given -mt, the object's MethodTable.
func clrPlayer(g game, data []region) {
	fmt.Println("\n== player scan, CLR field order ==")
	n := 0
	for _, r := range data {
		const chunk = 1 << 20
		for base := r.start; base < r.end; base += chunk {
			size := chunk
			if rem := int(r.end - base); rem < size {
				size = rem
			}
			buf := g.read(base, size+24)
			for i := 0; i+24 <= len(buf); i += 4 {
				v := make([]int32, 6)
				for k := range v {
					v[k] = int32(binary.LittleEndian.Uint32(buf[i+4*k:])) //nolint:gosec // a field, as its bits
				}
				v[0], v[1] = v[1], v[0] // statLifeMax is stored first
				if !validBlock(v) {
					continue
				}
				life := base + uint32(i) + 8
				obj := life - clrStatLife
				namePtr, _ := g.u32(obj + clrName)
				name, _, okName := g.clrString(namePtr)
				invPtr, _ := g.u32(obj + clrInventory)
				invLen, _ := g.u32(invPtr + 4)
				mt, _ := g.u32(obj)
				if !okName && n > 30 {
					continue
				}
				n++
				fmt.Printf("  life@%08x obj@%08x MT=%08x(match=%v) life=%d/%d/%d mana=%d/%d/%d name=%q inv len=%d\n",
					life, obj, mt, *playerMT != 0 && uint(mt) == *playerMT,
					v[2], v[1], v[0], v[3], v[4], v[5], name, invLen)
				if okName && invLen == 59 {
					items := g.read(invPtr, 0x18)
					fmt.Printf("    Item[] header: % x\n", items)
				}
			}
		}
	}
	fmt.Printf("  %d hits shown\n", n)
}
