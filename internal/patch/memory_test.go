package patch_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
A synthetic process map, read by both implementations.

The real thing comes from /proc, which cannot be planted, so both sides are
handed the same text instead: the Go through its own parser, the Python by
intercepting the one file it opens. What is being compared is the judgement made
about the map, not the reading of it.

The device mapping is in here on purpose. A graphics card's aperture is skipped
by the scans -- reading it can stall -- but it occupies its addresses as firmly as
anything else, and it is placed so that ignoring it moves where an arena would
go.
*/
const mapsListing = `08000000-08001000 r--p 00000000 00:00 0 
10000000-10004000 r-xp 00000000 00:00 0 
10004000-10008000 rwxp 00000000 00:00 0 
10008000-10018000 r--p 00000000 00:00 0 
10018000-10028000 rwxp 00000000 00:00 0 
10028000-10040000 r--s 00000000 00:06 9                                  /dev/nvidia0
30000000-30001000 r--p 00000000 00:00 0 
`

/*
The two decoys in that listing are the point of it.

An arena is found by its stamp, but only among mappings that could be one. The
first decoy is read-write-execute and the wrong size; the second is exactly the
right size, carries a stamp, comes first in the listing and is not executable. A
search that checked only the size would adopt the second and put stubs into a
region something else is using.
*/
const (
	arenaDecoy = 0x10008000 // right size, not executable, listed first
	arenaReal  = 0x10018000 // right size and read-write-execute
)

// mapped is a fake whose mappings are the listing above and whose bytes are a
// planted buffer.
type mapped struct{ *memtest.FakeMem }

func (m *mapped) AllRegions() []proc.Region {
	return proc.ParseAllRegions(strings.NewReader(mapsListing))
}

func (m *mapped) ExecRegions() []proc.Region {
	return proc.ParseExecRegions(strings.NewReader(mapsListing))
}

// pyMapped hands the Python the same listing in place of /proc.
func pyMapped(buf string) string {
	return fmt.Sprintf(`
import builtins, io, json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import patcher as P
MAPS = %q
_open = builtins.open
def fake_open(path, *a, **k):
    if str(path).endswith("/maps"):
        return io.StringIO(MAPS)
    return _open(path, *a, **k)
builtins.open = fake_open
mem = FakeMem(0x10000000, 0x30000)
mem.poke_bytes(0x10000000, b"\x90" * 0x30000)
%s
p = P.Patcher.__new__(P.Patcher)
p.mem = mem
p._sites, p._enabled, p._inj, p._values, p._arena = {}, set(), {}, {}, None
`, mapsListing, buf)
}

/*
newMapped is the Go side of the same, with the same planted bytes.

The buffer is filled with nops rather than left zeroed. A zeroed buffer is one
enormous run of the weaker padding the cave search falls back to, so every cave
question would answer "the start of the region" and nothing would be tested.
*/
func newMapped() *mapped {
	mem := memtest.New(0x10000000, 0x30000)
	mem.PokeBytes(0x10000000, bytes(0x90, 0x30000))
	return &mapped{mem}
}

/*
Where an arena would go is the same address, and the device mapping counts.

Placing an arena inside a mapping that is already there is not a failed
allocation: the game is asked to allocate at a fixed address, so the answer comes
back as a region that is not there and the bootstrap reports the game is not
running frames -- which sends the reader somewhere else entirely.
*/
func TestFreeBaseMatchesThePython(t *testing.T) {
	var want any
	askPython(t, pyMapped("")+
		fmt.Sprintf("print(json.dumps(p._free_base(%d)))", patch.ArenaSize), &want)

	got, err := patch.FreeBase(newMapped(), patch.ArenaSize)
	require.NoError(t, err)
	require.Equal(t, uint32(want.(float64)), got, "the arena would go somewhere else")
	require.Zero(t, got&0xFFFF, "and it is not 64KB-aligned")

	// The device mapping ends at 0x10040000 and the mapping before it at
	// 0x10028000, so an implementation that dropped it would answer 0x10030000.
	require.NotEqual(t, uint32(0x10030000), got, "the device mapping was treated as free")
}

// Which mapping an address is in, and whether it is in one at all.
func TestMappedMatchesThePython(t *testing.T) {
	addrs := []uint32{0x10000000, 0x10003FFF, 0x10004000, 0x1000FFFF, 0x20000000, 0x07FFFFFF}
	var want []any
	askPython(t, pyMapped("")+fmt.Sprintf(`
print(json.dumps([bool(p._mapped(a)) for a in %s]))`, pyInts(addrs)), &want)

	mem := newMapped()
	for i, addr := range addrs {
		_, ok := patch.Mapped(mem, addr)
		require.Equalf(t, want[i], ok, "%#x is mapped in one and not the other", addr)
	}
}

/*
An arena is recognised by its stamp, not by looking like one.

Memory the game handed back is indistinguishable from memory it handed to
somebody else, and adopting somebody else's would put stubs into a region they
are using.
*/
func TestFindingAnArenaMatchesThePython(t *testing.T) {
	for _, stamped := range []bool{true, false} {
		t.Run(fmt.Sprintf("stamped=%v", stamped), func(t *testing.T) {
			plant := ""
			if stamped {
				// Both decoy and candidate are stamped, so what is being tested
				// is which of them is *eligible*, not which one carries a mark.
				plant = fmt.Sprintf(`
mem.poke_bytes(%d + P.Patcher.ARENA_MAGIC_OFF, P.Patcher.ARENA_MAGIC)
mem.poke_bytes(%d + P.Patcher.ARENA_MAGIC_OFF, P.Patcher.ARENA_MAGIC)`, arenaDecoy, arenaReal)
			}
			var want map[string]any
			askPython(t, pyMapped(plant)+fmt.Sprintf(`
print(json.dumps({"found": p._find_arena(), "ok": p._arena_ok(%d)}))`, arenaReal), &want)

			mem := newMapped()
			if stamped {
				mem.PokeBytes(arenaDecoy+patch.ArenaMagicOff, patch.ArenaMagic)
				mem.PokeBytes(arenaReal+patch.ArenaMagicOff, patch.ArenaMagic)
			}
			found, ok := patch.FindArena(mem)
			require.Equal(t, want["ok"], patch.ArenaOK(mem, arenaReal), "disagree about the stamp")
			if want["found"] == nil {
				require.False(t, ok, "an arena was adopted that the Python would not")
				return
			}
			require.True(t, ok, "an arena was not adopted that the Python would")
			require.Equal(t, uint32(want["found"].(float64)), found, "a different arena")
			require.Equal(t, uint32(arenaReal), found, "and it is not the executable one")
		})
	}
}

/*
The cave search lands on the same bytes, and steps over what is already there.

Handing out space that already holds a stub writes one over the other, and what
runs afterwards is the splice. A disabled stub's cave is scrubbed to 0xCC, which
is exactly what the search hunts for, so what is installed has to be remembered
rather than inferred from the bytes.
*/
func TestFindingACaveMatchesThePython(t *testing.T) {
	const (
		padOne = 0x10000100 // a long run of int3
		padTwo = 0x10000400 // and another
		zeros  = 0x10002000 // a run of zeros, which is the weaker fallback
		size   = 32
	)

	for _, c := range []struct {
		name      string
		claimed   []uint32
		installed bool
	}{
		{name: "nothing taken"},
		{name: "the first run claimed in this pass", claimed: []uint32{padOne + 2}},
		{name: "the first run already installed", installed: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			pyState := "p._inj = {}"
			if c.installed {
				pyState = fmt.Sprintf(
					`p._inj = {"x": {"sites": [{"inject": 0, "cave": %d}], "stub_len": %d}}`,
					padOne+2, size)
			}
			var want any
			askPython(t, pyMapped(fmt.Sprintf(`
mem.poke_bytes(%d, b"\xcc" * 64)
mem.poke_bytes(%d, b"\xcc" * 64)
mem.poke_bytes(%d, b"\x00" * 64)`, padOne, padTwo, zeros))+fmt.Sprintf(`
%s
print(json.dumps(p._find_cave(%d, claimed=%s)))`, pyState, size, pyInts(c.claimed)), &want)

			mem := newMapped()
			mem.PokeBytes(padOne, bytes(0xCC, 64))
			mem.PokeBytes(padTwo, bytes(0xCC, 64))
			mem.PokeBytes(zeros, bytes(0x00, 64))

			state := patch.LoadState(-1)
			state.Inj = map[string]patch.Installed{}
			if c.installed {
				state.Inj["x"] = patch.Installed{
					Sites: []patch.Site{{Cave: padOne + 2}}, StubLen: size,
				}
			}

			got, err := patch.FindCave(patch.NewScanner(mem), size, c.claimed, false, state)
			require.NoError(t, err)
			require.Equal(t, uint32(want.(float64)), got, "a different cave was chosen")
		})
	}
}

// With nowhere to put a stub, both say so rather than picking somewhere.
func TestNoCaveIsAnError(t *testing.T) {
	var want bool
	askPython(t, pyMapped("")+`
try:
    p._find_cave(64)
    print(json.dumps(False))
except P.PatchError:
    print(json.dumps(True))`, &want)
	require.True(t, want, "the Python found a cave where there is no padding")

	_, err := patch.FindCave(patch.NewScanner(newMapped()), 64, nil, false, patch.LoadState(-1))
	require.Error(t, err, "a cave was found where the Python found none")
}

/*
Writing over the wrong bytes is refused, at the site and in the arena.

Both checks exist because both have caught something. The site check caught a
jump written 0x15 bytes early into Player.Update, which killed the game on the
next frame; the slot check is what refuses the next way of arriving at a stub
written over a live one.
*/
func TestTheWriteGuardsMatchThePython(t *testing.T) {
	const at = 0x10000200

	for _, c := range []struct {
		name  string
		bytes []byte
		site  []byte // what the site check expects to find
		slot  bool   // whether the slot check should accept it
	}{
		{"untouched code", []byte{0x90, 0x90, 0x90, 0x90}, []byte{0x90, 0x90, 0x90, 0x90}, false},
		{"something else there", []byte{0x90, 0x90, 0x90, 0x90}, []byte{0x8B, 0x45, 0x08, 0x90}, false},
		{"a fresh arena slot", []byte{0, 0, 0, 0}, []byte{0, 0, 0, 0}, true},
		{"a scrubbed slot", []byte{0xCC, 0xCC, 0xCC, 0xCC}, []byte{0xCC, 0xCC, 0xCC, 0xCC}, true},
		{"a slot holding a stub", []byte{0x60, 0x6A, 0x40, 0x00}, []byte{0x60, 0x6A, 0x40, 0x00}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want map[string]bool
			askPython(t, pyMapped(fmt.Sprintf("mem.poke_bytes(%d, %s)", at, pyBytes(c.bytes)))+
				fmt.Sprintf(`
def refused(fn):
    try:
        fn()
        return False
    except P.PatchError:
        return True
print(json.dumps({
    "site": refused(lambda: p._check_site(%d, %s, "t")),
    "slot": refused(lambda: p._check_slot(%d, 4, "t")),
}))`, at, pyBytes(c.site), at), &want)

			mem := newMapped()
			mem.PokeBytes(at, c.bytes)
			require.Equal(t, want["site"], patch.CheckSite(mem, at, c.site, "t") != nil,
				"disagree about whether the site may be patched")
			require.Equal(t, want["slot"], patch.CheckSlot(mem, at, 4, "t") != nil,
				"disagree about whether the slot may be written")
			require.Equal(t, c.slot, patch.CheckSlot(mem, at, 4, "t") == nil,
				"and it is not what the case says it is")
		})
	}
}
