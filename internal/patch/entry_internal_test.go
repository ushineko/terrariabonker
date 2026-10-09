package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Mono's in-place cheats are byte for byte what they were before they moved into
the version table.

On 2026-10-09 the four anchors and five in-place cheats moved from this package's
tables into layout's mono entry. Before the move, their patterns, ledgers, patch
offsets, original and patched bytes, the encoders' output for a spread of values,
and the player-field offsets were printed in this format and hashed. This
rebuilt the same text from the entry and got the same digest (commit 6f5fb2d):
the move changed where the numbers live and nothing else.

One deliberate change since: the ledgers dropped 1.4.5.7+24893155, a key that
named no real build (2026-10-09). The digest is of the data after that.
*/
func TestTheMonoCheatsAreWhatTheyWere(t *testing.T) {
	e := layout.Mono()
	var s string
	for _, k := range []string{"place", "pylon_place", "reset_block", "reset_minions"} {
		def := e.Anchors[k]
		s += fmt.Sprintf("%s|%s|%v|%v\n", k, MustParse(def.Pattern).String(), def.Unique, def.Verified)
	}
	for _, n := range []string{"fast_place", "max_minions", "mining", "pylons", "reach"} {
		site, c := e.Cheats[n], Cheats[n]
		s += fmt.Sprintf("%s|%s|%d|% X|% X", n, site.Anchor, site.PatchOff, site.Orig, site.Patched)
		if site.Encoder != "" {
			for _, v := range []int32{-3, 0, 1, 4, 10, 255} {
				s += fmt.Sprintf("|% X", encoders[site.Encoder](v))
			}
		}
		s += fmt.Sprintf("|%d|%v|%g|%g\n", e.PlayerValues[c.ValueField], c.ValueF32, c.OnValue, c.OffValue)
	}
	sum := sha256.Sum256([]byte(s))
	if got := hex.EncodeToString(sum[:]); got != "b3dadd0168e3bf14cb3613b2066e2c3a7566131415d04bc05c177a97bff29ce0" {
		t.Fatalf("the mono cheats changed in the move (digest %s):\n%s", got, s)
	}
}

// Every encoder a site names exists; an unknown name would panic at enable.
func TestEveryEncoderASiteNamesExists(t *testing.T) {
	for _, e := range layout.Entries {
		for name, site := range e.Cheats {
			if site.Encoder == "" {
				continue
			}
			if _, ok := encoders[site.Encoder]; !ok {
				t.Errorf("%s: %s names encoder %q, which does not exist", e.Name, name, site.Encoder)
			}
		}
	}
}

/*
Every build key in the version table and in every anchor's ledger is a declared
one (layout.SeenBuilds). A key spelt as a literal and misspelt matches nothing,
and nothing else would say so.
*/
func TestEveryBuildKeyIsADeclaredOne(t *testing.T) {
	declared := map[string]bool{}
	for _, b := range layout.SeenBuilds {
		declared[b] = true
	}
	check := func(where string, keys []string) {
		for _, k := range keys {
			if !declared[k] {
				t.Errorf("%s names build %q, which layout does not declare", where, k)
			}
		}
	}
	for _, e := range layout.Entries {
		check(e.Name+" builds", e.Builds)
		for key, a := range AnchorsFor(e) {
			check(e.Name+" anchor "+key, a.Verified)
			for _, v := range a.Variants {
				check(e.Name+" anchor "+key+" variant", []string{v.Build})
			}
		}
	}
}
