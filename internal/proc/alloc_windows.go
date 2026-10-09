package proc

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// virtualAllocEx is kernel32's VirtualAllocEx, which x/sys/windows does not wrap.
var virtualAllocEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("VirtualAllocEx")

/*
AllocateAt reserves and commits size bytes of read-write-execute memory in the
process at addr, which must be free.

Natively on Windows this program can allocate in the game itself. Under Proton it
cannot -- /proc/<pid>/mem moves bytes but maps nothing -- which is why the mono
injection set asks the game to call VirtualAlloc through a springboard. The
address is the caller's choice (patch.FreeBase) on both paths, so an arena lands
below 4 GB however the caller is built, and a 64-bit test can allocate in itself.
*/
func (m *Mem) AllocateAt(addr uint32, size int) error {
	h, err := m.handle()
	if err != nil {
		return err
	}
	got, _, callErr := virtualAllocEx.Call(uintptr(h), uintptr(addr), uintptr(size),
		windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_EXECUTE_READWRITE)
	if got == 0 {
		return fmt.Errorf("VirtualAllocEx at %#x: %w", addr, callErr)
	}
	if got != uintptr(addr) {
		return fmt.Errorf("VirtualAllocEx put %d bytes at %#x, not %#x", size, got, addr)
	}
	return nil
}
