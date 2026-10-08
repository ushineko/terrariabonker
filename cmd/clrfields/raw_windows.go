//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rawRuns prints every run of at least minRun records sharing their first
// dword, with no assumption about the rest: the measurement the FieldDesc model
// is checked against.
func rawRuns(h windows.Handle, minRun int, stride int) {
	var addr uintptr
	for addr < 0xFFFF0000 {
		var mbi windows.MemoryBasicInformation
		if windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)) != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if mbi.State == windows.MEM_COMMIT && mbi.Type == memPrivate && mbi.Protect&windows.PAGE_GUARD == 0 &&
			mbi.Protect&(windows.PAGE_READWRITE|windows.PAGE_READONLY|windows.PAGE_EXECUTE_READWRITE) != 0 {
			buf := make([]byte, mbi.RegionSize)
			var got uintptr
			_ = windows.ReadProcessMemory(h, mbi.BaseAddress, &buf[0], mbi.RegionSize, &got)
			buf = buf[:got]
			for i := 0; i+stride <= len(buf); i += 4 {
				d0 := binary.LittleEndian.Uint32(buf[i:])
				if d0 < 0x10000 || d0 == 0xFFFFFFFF {
					continue
				}
				j := i + stride
				for j+stride <= len(buf) && binary.LittleEndian.Uint32(buf[j:]) == d0 &&
					binary.LittleEndian.Uint32(buf[j+4:]) != binary.LittleEndian.Uint32(buf[j+4-stride:]) {
					j += stride
				}
				n := (j - i) / stride
				if n >= minRun {
					fmt.Printf("run at %08x  first=%08x  n=%d\n", uint32(mbi.BaseAddress)+uint32(i), d0, n) //nolint:gosec // a 32-bit process's address
					for k := 0; k < 6 && k < n; k++ {
						at := i + k*stride
						fmt.Printf("   % x\n", buf[at:at+stride])
					}
					i = j - 4
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
}
