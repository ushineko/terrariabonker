package patch_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
Every anchor is the Python's anchor, and there are no others on either side.

These bytes are searched for in a running game and then written over. A byte
typed wrong here does not fail: it matches somewhere else, or matches nothing
and takes a cheat offline, and the first of those corrupts the game. The
patterns were derived once, by hand, against a disassembly, and re-deriving them
is days of work -- so they are carried across as data and the whole table is
compared, by name, by bytes, by wildcard and by provenance.

Both directions, so an anchor added on one side and missed on the other fails
rather than diverging quietly.
*/
func TestEveryAnchorMatchesThePython(t *testing.T) {
	var want map[string]struct {
		Pattern  string   `json:"pattern"`
		Verified []string `json:"verified"`
		Unique   bool     `json:"unique"`
		Variants []string `json:"variants"`
		SeedOff  int      `json:"seed_off"`
		Seed     string   `json:"seed"`
	}
	askPython(t, `
import json
from terrariabonker import patcher as P

def render(pat):
    return " ".join("??" if not m else "%02X" % b for b, m in zip(pat.raw, pat.mask))

out = {}
for key, a in P.ANCHORS.items():
    off, seed = a.pattern.seed()
    out[key] = {
        "pattern": render(a.pattern),
        "verified": sorted(a.verified),
        "unique": a.unique,
        "variants": [b for b, _ in a.variants],
        "seed_off": off,
        "seed": seed.hex(),
    }
print(json.dumps(out))
`, &want)
	require.NotEmpty(t, want)

	for key, w := range want {
		got, known := patch.Anchors[key]
		require.Truef(t, known, "%s is an anchor the Python has and this does not", key)
		require.Equalf(t, w.Pattern, got.Pattern.String(), "%s has different bytes", key)

		verified := append([]string{}, got.Verified...)
		sort.Strings(verified)
		require.Equalf(t, w.Verified, verified, "%s claims different builds", key)
		require.Equalf(t, w.Unique, got.Unique, "%s disagrees about patching twins", key)

		builds := []string{}
		for _, v := range got.Variants {
			builds = append(builds, v.Build)
		}
		require.Equalf(t, w.Variants, builds, "%s has different variants", key)

		// The seed is what the scan actually searches for, so it is compared
		// rather than trusted to fall out of the bytes being equal.
		off, seed := got.Pattern.Seed()
		require.Equalf(t, w.SeedOff, off, "%s seeds the scan at a different place", key)
		require.Equalf(t, w.Seed, hexOf(seed), "%s seeds the scan with different bytes", key)
	}
	for key := range patch.Anchors {
		_, known := want[key]
		require.Truef(t, known, "%s is an anchor this has and the Python does not", key)
	}
}

/*
The patterns match, and fail to match, in the same places.

Seed and Matches are the whole of the scan, and they are compared over the
anchors' own bytes rather than over something invented: a buffer is built from
each pattern with its wildcards filled in, the match is checked where it should
be found, and then each fixed byte in turn is corrupted and the match has to
fail. A wildcard mask that is one position out passes the first of those and
fails the second.
*/
func TestMatchingBehavesLikeThePython(t *testing.T) {
	var want map[string][]bool
	askPython(t, `
import json
from terrariabonker import patcher as P
out = {}
for key, a in P.ANCHORS.items():
    pat = a.pattern
    buf = bytes((b if m else 0xCC) for b, m in zip(pat.raw, pat.mask))
    verdicts = [pat.matches(buf, 0), pat.matches(buf, 1), pat.matches(buf, -1)]
    for i in range(len(buf)):
        bad = bytearray(buf)
        bad[i] ^= 0xFF
        verdicts.append(pat.matches(bytes(bad), 0))
    out[key] = verdicts
print(json.dumps(out))
`, &want)

	for key, w := range want {
		anchor := patch.Anchors[key]
		pat := anchor.Pattern

		// Every wildcard filled with a byte the pattern cannot be asking for.
		buf := make([]byte, pat.Len())
		for i := range buf {
			buf[i] = 0xCC
			if pat.Mask[i] {
				buf[i] = pat.Raw[i]
			}
		}
		got := []bool{pat.Matches(buf, 0), pat.Matches(buf, 1), pat.Matches(buf, -1)}
		for i := range buf {
			bad := append([]byte{}, buf...)
			bad[i] ^= 0xFF
			got = append(got, pat.Matches(bad, 0))
		}
		require.Equalf(t, w, got, "%s matches in different places", key)
	}
}

// Parsing a pattern is parsing the same pattern, including the shapes that are
// not anchors: an empty one, one that is all wildcards, one byte.
func TestParsingMatchesThePython(t *testing.T) {
	cases := []string{"", "??", "90", "?? 90 ??", "00 00 00", "FF"}
	var want []map[string]any
	askPython(t, `
import json
from terrariabonker import patcher as P
out = []
for s in `+quote(cases)+`:
    pat = P._pat(s)
    off, seed = pat.seed()
    out.append({"len": len(pat.raw), "mask": list(pat.mask),
                "seed_off": off, "seed": seed.hex()})
print(json.dumps(out))
`, &want)

	for i, s := range cases {
		pat := patch.MustParse(s)
		off, seed := pat.Seed()
		mask := make([]int, len(pat.Mask))
		for j, m := range pat.Mask {
			if m {
				mask[j] = 1
			}
		}
		require.Equalf(t, want[i]["len"], asJSON(t, pat.Len()), "%q is a different length", s)
		require.Equalf(t, want[i]["mask"], asJSON(t, mask), "%q wildcards differently", s)
		require.Equalf(t, want[i]["seed_off"], asJSON(t, off), "%q seeds elsewhere", s)
		require.Equalf(t, want[i]["seed"], hexOf(seed), "%q seeds differently", s)
	}
}

/*
The order patterns are tried in is the same.

A build with its own variant has it tried first, and every other pattern is
still tried after it, because an unverified build usually matches a pattern
derived for a different one. Getting this backwards would disable working cheats
on exactly the builds nobody has checked yet.
*/
func TestCandidateOrderMatchesThePython(t *testing.T) {
	// Three different patterns, so the order they come back in is visible.
	// With the base pattern equal to one of the variants, dedup makes every
	// ordering look the same -- which is how the first version of this test
	// passed with the order reversed.
	const base, a, b = "AA ?? AA", "90 90 ?? 90", "CC CC ?? CC"
	for _, build := range []string{"", "one", "two", "nobody"} {
		t.Run(build, func(t *testing.T) {
			var want []string
			askPython(t, `
import json
from terrariabonker import patcher as P
anchor = P.Anchor(P._pat(`+pyStr(base)+`),
                  variants=(("one", P._pat(`+pyStr(a)+`)), ("two", P._pat(`+pyStr(b)+`))))
def render(pat):
    return " ".join("??" if not m else "%02X" % x for x, m in zip(pat.raw, pat.mask))
print(json.dumps([render(p) for p in anchor.candidates(`+pyBuild(build)+`)]))
`, &want)

			anchor := patch.Anchor{
				Pattern: patch.MustParse(base),
				Variants: []patch.Variant{
					{Build: "one", Pattern: patch.MustParse(a)},
					{Build: "two", Pattern: patch.MustParse(b)},
				},
			}
			var got []string
			for _, p := range anchor.Candidates(build) {
				got = append(got, p.String())
			}
			require.Equal(t, want, got, "the patterns are tried in a different order")
		})
	}
}
