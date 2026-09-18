package patch_test

import (
	"fmt"
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

// pyPlant is the same planted code, written with the Python's own anchor table.
func pyPlant() string {
	return fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import patcher as P
pat = P.ANCHORS[%q].pattern
body = bytes((b if m else 0xCC) for b, m in zip(pat.raw, pat.mask))
first = next(i for i, m in enumerate(pat.mask) if m)
miss = bytearray(body); miss[first] ^= 0xFF
mem = FakeMem(%d, %d)
mem.poke_bytes(%d, body)
mem.poke_bytes(%d, body)
mem.poke_bytes(%d, bytes(miss))
p = P.Patcher(mem)
p._exec_regions = lambda writable=False: [(%d, %d)]
`, probe, codeBase, codeSize, hitOne, hitTwo, nearMiss, codeBase, codeBase+codeSize)
}

// Both find both copies and neither takes the near miss.
func TestScanningMatchesThePython(t *testing.T) {
	var want []uint32
	askPython(t, pyPlant()+fmt.Sprintf("print(json.dumps(sorted(p._scan(%q))))", probe), &want)
	require.Equal(t, []uint32{hitOne, hitTwo}, want, "the Python found something else")

	sites := patch.NewScanner(plantCode(t)).Scan(probe, "")
	require.Equal(t, want, sites, "the two scans found different sites")
}

/*
Several matches is a normal answer, not a failure.

mono JITs one method into more than one arena and the copies are identical where
this patches, so both are used. Only an anchor declared unique treats a second
match as a reason to stop -- and it does stop, rather than picking one, because
patching the wrong twin is what "unique" is there to prevent.
*/
func TestResolvingMatchesThePython(t *testing.T) {
	for _, unique := range []bool{false, true} {
		t.Run(fmt.Sprintf("unique=%v", unique), func(t *testing.T) {
			var want struct {
				Sites     []uint32 `json:"sites"`
				Available bool     `json:"available"`
				Reason    string   `json:"reason"`
				Verified  bool     `json:"verified"`
			}
			askPython(t, pyPlant()+fmt.Sprintf(`
a = P.ANCHORS[%q]
P.ANCHORS[%q] = P.Anchor(a.pattern, verified=a.verified, unique=%v)
res = p.resolution(%q)
print(json.dumps({"sites": list(res.sites), "available": res.available,
                  "reason": res.reason, "verified": res.verified}))`,
				probe, probe, pyBool(unique), probe), &want)

			restore := patch.Anchors[probe]
			t.Cleanup(func() { patch.Anchors[probe] = restore })
			patch.Anchors[probe] = patch.Anchor{
				Pattern: restore.Pattern, Verified: restore.Verified, Unique: unique,
			}

			got := patch.NewScanner(plantCode(t)).Resolve(probe, "")
			require.Equal(t, want.Sites, got.Sites, "different sites")
			require.Equal(t, want.Available, got.Available, "disagree about usability")
			require.Equal(t, want.Verified, got.Verified, "disagree about provenance")
			if !want.Available {
				require.NotEmpty(t, got.Reason, "refused without saying why")
				require.Contains(t, got.Reason, "unique", "and not for the stated reason")
			}
		})
	}
}

/*
An anchor that matches nothing says so, and says both of the things it could
mean.

A method that has moved in this build and a method that has not been JIT'd yet
look identical from here, and naming only one of them sends the reader the wrong
way -- which is worth stating, because the live game has an anchor in exactly
that state right now.
*/
func TestAnAnchorThatMatchesNothing(t *testing.T) {
	var want struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import patcher as P
mem = FakeMem(%d, %d)
p = P.Patcher(mem)
p._exec_regions = lambda writable=False: [(%d, %d)]
res = p.resolution(%q)
print(json.dumps({"available": res.available, "reason": res.reason}))`,
		codeBase, codeSize, codeBase, codeBase+codeSize, probe), &want)
	require.False(t, want.Available)

	mem := &codeMem{memtest.New(codeBase, codeSize)}
	got := patch.NewScanner(mem).Resolve(probe, "")
	require.Equal(t, want.Available, got.Available, "disagree about an empty region")
	require.Empty(t, got.Sites, "sites were reported for nothing")
	require.Contains(t, got.Reason, "matched nothing")
	require.Contains(t, got.Reason, "JIT", "the reason names only one of the two causes")
}

/*
Provenance is reported for the build that is running, and only for it.

An anchor verified on another build still resolves and is still used -- the
ledger is not a gate -- but it is reported as unproven, which is what the window
marks.
*/
func TestVerifiedIsReportedPerBuild(t *testing.T) {
	known := patch.Anchors[probe].Verified[0]
	for _, build := range []string{known, "9.9.9+1", ""} {
		t.Run(build, func(t *testing.T) {
			var want bool
			askPython(t, pyPlant()+fmt.Sprintf(
				"print(json.dumps(p.resolution(%q, %s).verified))", probe, pyBuild(build)), &want)

			got := patch.NewScanner(plantCode(t)).Resolve(probe, build)
			require.Equal(t, want, got.Verified, "disagree about whether %q is proven", build)
			require.True(t, got.Available, "an unproven build stopped the anchor being used")
		})
	}
}

/*
Every candidate pattern is tried, not just the first.

An anchor carries a variant when a build moved the code it patches, and the
variant that does not match this build has to fall through to the one that does.
Stopping at the first empty result takes the cheat offline on every build except
the newest.
*/
func TestEveryCandidateIsTried(t *testing.T) {
	const missing = "DE AD BE EF DE AD BE EF"

	var want []uint32
	askPython(t, pyPlant()+fmt.Sprintf(`
a = P.ANCHORS[%q]
P.ANCHORS[%q] = P.Anchor(P._pat(%q), verified=a.verified,
                         variants=(("elsewhere", a.pattern),))
print(json.dumps(sorted(p._scan(%q))))`, probe, probe, missing, probe), &want)
	require.Equal(t, []uint32{hitOne, hitTwo}, want,
		"the Python did not fall through to the matching pattern")

	// Planted first, and only then is the anchor swapped: plantCode reads the
	// anchor it is planting, so swapping first would plant the pattern that is
	// meant not to match and the fall-through would never be exercised.
	mem := plantCode(t)

	restore := patch.Anchors[probe]
	t.Cleanup(func() { patch.Anchors[probe] = restore })
	patch.Anchors[probe] = patch.Anchor{
		Pattern:  patch.MustParse(missing),
		Verified: restore.Verified,
		Variants: []patch.Variant{{Build: "elsewhere", Pattern: restore.Pattern}},
	}

	sites := patch.NewScanner(mem).Scan(probe, "")
	require.Equal(t, want, sites, "a candidate after the first was not tried")
}

/*
Matches that overlap each other are all found.

A scan that resumed past the end of what it just matched would report one where
there are two. No anchor in the table overlaps itself today, but nothing stops
one doing so -- a pattern of repeated bytes is exactly what a run of padding or
a table of identical stubs looks like -- and the failure would be a cheat
patching half the sites it should.
*/
func TestOverlappingMatchesAreAllFound(t *testing.T) {
	const repeated = "AA AA AA"

	var want []uint32
	askPython(t, fmt.Sprintf(`
import json, os, sys
sys.path.insert(0, os.getcwd())
sys.path.insert(0, os.path.join(os.getcwd(), "tests"))
from conftest import FakeMem
from terrariabonker import patcher as P
P.ANCHORS["overlapping"] = P.Anchor(P._pat(%q))
mem = FakeMem(%d, %d)
mem.poke_bytes(%d, bytes([0xAA] * 5))
p = P.Patcher(mem)
p._exec_regions = lambda writable=False: [(%d, %d)]
print(json.dumps(sorted(p._scan("overlapping"))))`,
		repeated, codeBase, codeSize, hitOne, codeBase, codeBase+codeSize), &want)
	require.Len(t, want, 3, "the Python found a different number of overlapping matches")

	t.Cleanup(func() { delete(patch.Anchors, "overlapping") })
	patch.Anchors["overlapping"] = patch.Anchor{Pattern: patch.MustParse(repeated)}

	mem := memtest.New(codeBase, codeSize)
	mem.PokeBytes(hitOne, []byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA})
	sites := patch.NewScanner(&codeMem{mem}).Scan("overlapping", "")
	require.Equal(t, want, sites, "overlapping matches were counted differently")
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
