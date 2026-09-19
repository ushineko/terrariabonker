package patch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
The scan finds the same sites, and refuses in the same cases.

Where a pattern is judged to match is where bytes get written, so this is
compared over planted code that has all three cases in it: an exact match, a
second identical copy -- which is normal, mono JITs one method into more than one
arena -- and a near miss that differs in one fixed byte. A scan that ignored the
mask would take the near miss, and a scan that searched for the whole pattern
rather than its longest fixed run would find nothing at all.
*/

const (
	codeBase = 0x30000000
	codeSize = 0x1000
	// Where the three candidates go. They are far enough apart that one cannot
	// be found inside another.
	hitOne   = codeBase + 0x100
	hitTwo   = codeBase + 0x400
	nearMiss = codeBase + 0x700
	/*
		reset_minions is the probe because its longest fixed run is at the *end*:
		six fixed bytes, four wildcards, then ten fixed. So the scan seeds on the
		trailing run and has to step back to the match, and the near miss flips a
		byte in the leading run -- one the seed search never looks at. A scan that
		did not subtract the seed offset reports the wrong address, and one that
		trusted the seed instead of checking the whole pattern takes the near
		miss. With a pattern whose run is at the front, both of those pass.
	*/
	probe = "reset_minions"
)

// plantCode is a code region with the two matches and the near miss in it.
func plantCode(t *testing.T) *codeMem {
	t.Helper()
	pat := patch.Anchors[probe].Pattern
	mem := memtest.New(codeBase, codeSize)
	body := filled(pat)
	mem.PokeBytes(hitOne, body)
	mem.PokeBytes(hitTwo, body)

	// One fixed byte flipped: everything else about it still looks right.
	miss := append([]byte{}, body...)
	miss[firstFixed(pat)] ^= 0xFF
	mem.PokeBytes(nearMiss, miss)
	return &codeMem{mem}
}

// filled is the pattern's own bytes with every wildcard set to something the
// pattern cannot be asking for.
func filled(pat patch.Pattern) []byte {
	out := make([]byte, pat.Len())
	for i := range out {
		out[i] = 0xCC
		if pat.Mask[i] {
			out[i] = pat.Raw[i]
		}
	}
	return out
}

// firstFixed is the first position the pattern insists on.
func firstFixed(pat patch.Pattern) int {
	for i, m := range pat.Mask {
		if m {
			return i
		}
	}
	return 0
}

// codeMem is a fake whose whole buffer is executable, which is what a scan looks
// at.
type codeMem struct{ *memtest.FakeMem }

func (m *codeMem) ExecRegions() []proc.Region {
	return []proc.Region{{Start: codeBase, End: codeBase + codeSize}}
}

/*
Sites, once resolved, are kept.

The scan reads every executable mapping in the process, over a gigabyte of it,
and the window asks for patch status on a timer. Re-scanning each time is the
difference between a status that is instant and one that takes eight seconds.
*/
func TestResolvedSitesAreKept(t *testing.T) {
	mem := plantCode(t)
	scanner := patch.NewScanner(mem)

	first := scanner.Resolve(probe, "")
	require.True(t, first.Available)
	require.Len(t, first.Sites, 2)

	// The code is gone. A fresh scan would find nothing; the kept answer stands.
	mem.PokeBytes(hitOne, make([]byte, 0x400))
	mem.PokeBytes(hitTwo, make([]byte, 0x400))
	require.Empty(t, patch.NewScanner(mem).Scan(probe, ""), "the code was not really overwritten")

	again := scanner.Resolve(probe, "")
	require.Equal(t, first.Sites, again.Sites, "the sites were scanned for again")
}
