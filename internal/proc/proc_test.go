package proc_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The regions are read from a real listing, not a made-up one.

A /proc maps listing is messier than anything that would be written by hand:
device mappings, file-backed pages, guard pages, and under Proton a mixture of
32-bit and 64-bit addresses in one process. So the test hands both
implementations the same real listing -- this machine's -- and compares what each
makes of it.
*/

const pythonTimeout = 2 * time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

/*
Both implementations make the same regions of the same listing.

The listing is the Python's own process, which has everything awkward in it: a
64-bit address space, file-backed mappings, and whatever the interpreter has
loaded. Its raw text comes back with the answer so the Go side parses exactly
what the Python parsed.
*/
func TestTheSameListingGivesTheSameRegions(t *testing.T) {
	var got struct {
		Maps    string    `json:"maps"`
		Regions [][]int64 `json:"regions"`
	}
	askPython(t, `
import json, os
from terrariabonker.proc import Mem
pid = os.getpid()
print(json.dumps({
    "maps": open(f"/proc/{pid}/maps").read(),
    "regions": Mem(pid).regions(),
}))
`, &got)
	require.NotEmpty(t, got.Maps)

	// The Python keeps every region; this one keeps the 32-bit ones, because a
	// 32-bit game has no others and reading a 64-bit address into a uint32 is
	// how an address becomes a different address.
	var want [][]int64
	for _, r := range got.Regions {
		if r[0] <= 0xFFFFFFFF && r[1] <= 0xFFFFFFFF {
			want = append(want, r)
		}
	}

	regions := proc.ParseRegions(strings.NewReader(got.Maps))
	require.Len(t, regions, len(want), "a different number of regions was kept")
	for i, r := range want {
		require.Equalf(t, uint32(r[0]), regions[i].Start, "region %d starts elsewhere", i)
		require.Equalf(t, uint32(r[1]), regions[i].End, "region %d ends elsewhere", i)
		require.Equalf(t, int(r[1]-r[0]), regions[i].Size(), "region %d is a different size", i)
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

/*
The game is found by what it maps, not by what it is called.

Proton's wrapper scripts name Terraria.exe on their command lines; only the game
maps it executable. Skipped when the game is not running, because there is
nothing to agree about.
*/
func TestFindingTheGameAgreesWithThePython(t *testing.T) {
	var want any
	askPython(t, `
import json
from terrariabonker import proc
try:
    print(json.dumps(proc.find_pid()))
except proc.ProcError:
    print(json.dumps(None))
`, &want)

	pid, err := proc.FindPID()
	if want == nil {
		require.ErrorIs(t, err, proc.ErrNoGame, "the game is not running for either of us")
		return
	}
	require.NoError(t, err)
	require.Equal(t, int(want.(float64)), pid)

	// And the same file behind it.
	var path any
	askPython(t, `
import json
from terrariabonker import proc
print(json.dumps(proc.Mem(proc.find_pid()).exe_path()))
`, &path)
	require.Equal(t, path, proc.New(pid).ExePath())

	// Its regions are readable without privilege, which is what makes a scan
	// plannable before anything is elevated.
	require.NotEmpty(t, proc.New(pid).Regions())
	_, err = os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "mem"))
	require.NoError(t, err)
}
