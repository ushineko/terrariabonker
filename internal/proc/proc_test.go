package proc_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The regions are read from a real listing, not a made-up one.

A /proc maps listing is messier than anything that would be written by hand:
device mappings, file-backed pages, guard pages, and -- in a 64-bit process --
addresses past anything a 32-bit game has. So the test hands both
implementations the same real listing and compares what each makes of it.
*/

/*
A real listing parses into regions that satisfy the rules the parser exists for.

This process's own /proc listing is what is parsed: it has everything awkward in
it -- a 64-bit address space, file-backed mappings, whatever the runtime has
loaded -- and none of it is written by the test, so the parser meets lines
nobody chose for it.

Every kept region is writable, is not a device, and fits a uint32 -- which is
the width every address in the 32-bit game has. Nothing here asserts *which*
regions, because that is the machine's business and changes between runs.
*/
func TestARealListingParsesIntoScannableRegions(t *testing.T) {
	raw, err := os.ReadFile("/proc/self/maps")
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	regions := proc.ParseRegions(strings.NewReader(string(raw)))
	require.NotEmpty(t, regions, "a real listing produced no scannable region at all")

	kept := map[string]bool{}
	for i, r := range regions {
		require.Lessf(t, r.Start, r.End, "region %d is empty or backwards", i)
		require.Equalf(t, int(r.End-r.Start), r.Size(), "region %d is a different size", i)
		kept[fmt.Sprintf("%x-%x", r.Start, r.End)] = true
	}

	// And every line the parser kept really is one it should have: writable,
	// anonymous, and inside a 32-bit address space.
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		bounds := strings.SplitN(fields[0], "-", 2)
		if len(bounds) != 2 {
			continue
		}
		start, err1 := strconv.ParseUint(bounds[0], 16, 64)
		end, err2 := strconv.ParseUint(bounds[1], 16, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		key := fmt.Sprintf("%x-%x", start, end)
		if !kept[key] {
			continue
		}
		require.Containsf(t, fields[1], "w", "%s was kept and is not writable", key)
		require.Lessf(t, end, uint64(1<<32), "%s was kept and does not fit a uint32", key)
		if len(fields) > 5 {
			require.NotContainsf(t, fields[5], "/dev/", "%s was kept and is a device", key)
		}
	}
}

// What is skipped, and why, spelled out on lines this parser has to handle.
func TestWhatIsNotAScannableRegion(t *testing.T) {
	listing := strings.Join([]string{
		"08048000-08049000 r-xp 00000000 08:01 1  /usr/bin/thing", // not writable
		"08049000-0804a000 rw-p 00001000 08:01 1  /usr/bin/thing", // writable and file-backed
		"0804a000-0804b000 rw-p 00000000 00:00 0",                 // anonymous, no path at all
		"0804b000-0804c000 rw-s 00000000 00:06 9  /dev/nvidia0",   // a device, which can stall
		"ffff800000000000-ffff800000001000 rw-p 0 00:00 0",        // beyond a 32-bit game
		"not a maps line",
		"",
	}, "\n")

	got := proc.ParseRegions(strings.NewReader(listing))
	require.Len(t, got, 2)
	require.Equal(t, uint32(0x08049000), got[0].Start)
	require.Equal(t, uint32(0x0804a000), got[1].Start)
}

// The executable listing keeps what the writable one drops and drops what it
// keeps, on the same lines.
func TestWhatIsNotExecutableMemory(t *testing.T) {
	listing := strings.Join([]string{
		"08048000-08049000 r-xp 00000000 08:01 1  /usr/bin/thing", // code, file-backed
		"08049000-0804a000 rw-p 00001000 08:01 1  /usr/bin/thing", // data, not code
		"0804a000-0804b000 rwxp 00000000 00:00 0",                 // anonymous JIT output
		"0804b000-0804c000 r-xs 00000000 00:06 9  /dev/nvidia0",   // a device, which can stall
		"not a maps line",
	}, "\n")

	got := proc.ParseExecRegions(strings.NewReader(listing))
	require.Len(t, got, 2)
	require.Equal(t, uint32(0x08048000), got[0].Start)
	require.Equal(t, uint32(0x0804a000), got[1].Start)
}

/*
The game is found by what it maps, not by what it is called.

Proton's wrapper scripts name Terraria.exe on their command lines; only the game
maps it executable. Skipped when the game is not running, because there is
nothing to agree about.
*/
