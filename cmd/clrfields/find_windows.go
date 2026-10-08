//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// findSequential looks for consecutive RIDs of one type at any stride from 4
// to 32 bytes, reporting where and with what stride: a measurement of the
// FieldDesc list's shape that assumes only that RIDs appear in order.
func findSequential(h windows.Handle, t Type) {
	lo, hi := t.Fields[0].RID, t.Fields[len(t.Fields)-1].RID
	hist := map[int]int{}
	shown := 0
	var addr uintptr
	for addr < 0xFFFF0000 {
		var mbi windows.MemoryBasicInformation
		if windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)) != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if mbi.State == windows.MEM_COMMIT && mbi.Type == memPrivate && mbi.Protect&windows.PAGE_GUARD == 0 &&
			mbi.Protect&(windows.PAGE_READWRITE|windows.PAGE_READONLY|windows.PAGE_EXECUTE_READWRITE|windows.PAGE_WRITECOPY) != 0 {
			buf := make([]byte, mbi.RegionSize)
			var got uintptr
			_ = windows.ReadProcessMemory(h, mbi.BaseAddress, &buf[0], mbi.RegionSize, &got)
			buf = buf[:got]
			for i := 0; i+4 <= len(buf); i += 4 {
				w := binary.LittleEndian.Uint32(buf[i:])
				r := w & ridMask
				if r < lo || r >= hi-2 {
					continue
				}
				for st := 4; st <= 32; st += 4 {
					if i+2*st+4 > len(buf) {
						break
					}
					a := binary.LittleEndian.Uint32(buf[i+st:]) & ridMask
					b := binary.LittleEndian.Uint32(buf[i+2*st:]) & ridMask
					if a == r+1 && b == r+2 {
						hist[st]++
						if shown < 40 && st == 12 {
							shown++
							s := i - 8
							if s < 0 {
								s = 0
							}
							fmt.Printf("%08x stride %d type=%x  rid %d=%s\n   % x\n", uint32(mbi.BaseAddress)+uint32(i), st, mbi.Type, r, //nolint:gosec // a 32-bit process's address
								t.Fields[r-lo].Name, buf[s:i+3*st])
						}
					}
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	fmt.Println("hits by stride:", hist)
}
