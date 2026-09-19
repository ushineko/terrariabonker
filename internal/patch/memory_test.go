package patch_test

import (
	"strings"

	"github.com/ushineko/terrariabonker/internal/memtest"
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
