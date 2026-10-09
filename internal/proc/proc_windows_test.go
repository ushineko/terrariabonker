package proc_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
lowPage is a page of this process's own memory below 4 GB, and its size.

The test binary is 64-bit and its heap is far above 4 GB, where a uint32
address cannot reach -- and every address here is a uint32, because the game is
32-bit. So the test asks Windows for a page at a low address and points
proc.Mem at its own pid: the real ReadProcessMemory, WriteProcessMemory and
VirtualQueryEx path, with no game.
*/
func lowPage(t *testing.T) (uint32, uint32) {
	t.Helper()
	const size = 0x10000
	for base := uintptr(0x10000000); base < 0x80000000; base += 0x01000000 {
		addr, err := windows.VirtualAlloc(base, size, windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE) })
		return uint32(addr), size //nolint:gosec // allocated below 4 GB
	}
	t.Skip("no free address below 4 GB to allocate a test page at")
	return 0, 0
}

/*
plant and observe put bytes into the page and read them back without going
through proc.Mem: straight Win32 calls on this process's own pseudo-handle. They
are the independent side of each assertion -- a test that wrote with Mem and read
with Mem would pass with both pointed at the wrong address.
*/
func plant(t *testing.T, addr uint32, data []byte) {
	t.Helper()
	var n uintptr
	require.NoError(t, windows.WriteProcessMemory(windows.CurrentProcess(), uintptr(addr), &data[0], uintptr(len(data)), &n))
}

func observe(t *testing.T, addr uint32, size int) []byte {
	t.Helper()
	buf := make([]byte, size)
	var n uintptr
	require.NoError(t, windows.ReadProcessMemory(windows.CurrentProcess(), uintptr(addr), &buf[0], uintptr(size), &n))
	return buf[:n]
}

func TestMemReadsAndWritesARealProcess(t *testing.T) {
	addr, _ := lowPage(t)
	plant(t, addr, []byte("terrariabonker"))
	mem := proc.New(os.Getpid())

	require.Equal(t, []byte("terrariabonker"), mem.Read(addr, 14))

	require.True(t, mem.Write(addr+4, []byte("ABCD")))
	require.Equal(t, []byte("terrABCDbonker"), observe(t, addr, 14), "the write did not land where it was aimed")

	v, ok := mem.ReadU32(addr + 4)
	require.True(t, ok)
	require.Equal(t, uint32(0x44434241), v)
}

// A read that runs off the end of a region comes back short rather than not at
// all, which is what /proc/<pid>/mem does and what a scanner relies on.
func TestAReadPastTheEndOfARegionComesBackShort(t *testing.T) {
	addr, size := lowPage(t)
	end := addr + size
	got := proc.New(os.Getpid()).Read(end-8, 64)
	require.Len(t, got, 8)
}

// The page is listed as writable, scannable memory and not as code, and is
// among the taken space.
func TestTheRegionListsSeeAWritablePage(t *testing.T) {
	addr, size := lowPage(t)
	mem := proc.New(os.Getpid())
	contains := func(rs []proc.Region) (proc.Region, bool) {
		for _, r := range rs {
			if r.Start <= addr && addr < r.End {
				return r, true
			}
		}
		return proc.Region{}, false
	}

	r, ok := contains(mem.Regions())
	require.True(t, ok, "the page is not among the writable regions")
	require.True(t, r.Writable && r.Readable && !r.Executable, "%+v", r)
	require.GreaterOrEqual(t, r.End-addr, size)

	_, ok = contains(mem.ExecRegions())
	require.False(t, ok, "a read-write page is listed as code")

	_, ok = contains(mem.AllRegions())
	require.True(t, ok, "the page is not among the taken space")
}

func TestTheExecutableIsThisProcess(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	got := proc.New(os.Getpid()).ExePath()
	require.True(t, bytes.EqualFold([]byte(got), []byte(self)), "ExePath %q, want %q", got, self)
}
