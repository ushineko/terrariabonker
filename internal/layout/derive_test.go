package layout

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
A derived entry is the base with the edits, and the base is untouched.

The edit is the one the version table exists for: a later build that recompiled
the method one cheat patches. Every kind of shared storage is written through --
a map entry replaced, a map entry added, a slice appended to, a slice's element
overwritten -- and the base must print exactly as it did.
*/
func TestADerivedEntryLeavesItsBaseAlone(t *testing.T) {
	before := fmt.Sprintf("%#v", monoEntry)
	next := Derive(monoEntry, func(e *Entry) {
		e.Name = "wine-mono-next"
		e.Builds = append(e.Builds, "1.4.5.9+1")
		e.Anchors["reset_block_next"] = AnchorDef{Pattern: "90 90"}
		site := e.Cheats["mining"]
		site.Anchor = "reset_block_next"
		site.Orig[0] = 0xCC
		e.Cheats["mining"] = site
		def := e.Anchors["reset_block"]
		def.Verified[0] = "overwritten"
		e.PlayerValues["pickSpeed"] = 1
		e.Reads[0] = "nothing"
	})
	require.Equal(t, before, fmt.Sprintf("%#v", monoEntry), "deriving changed the base")
	require.Equal(t, "reset_block_next", next.Cheats["mining"].Anchor)
	require.Equal(t, monoEntry.Cheats["reach"], next.Cheats["reach"], "an unchanged cheat was not carried over")
	require.Equal(t, monoEntry.Player, next.Player)
}

// Every list an entry has is its own after deriving, under every entry: an
// element overwritten in the derived entry is not overwritten in the base.
func TestADerivedEntrysListsAreItsOwn(t *testing.T) {
	for _, base := range Entries {
		before := fmt.Sprintf("%#v", base)
		Derive(base, func(e *Entry) {
			for _, list := range [][]string{e.Builds, e.Versions, e.Confirmed} {
				if len(list) > 0 {
					list[0] = "overwritten"
				}
			}
			for _, list := range [][]Feature{e.Reads, e.WriteFeatures} {
				if len(list) > 0 {
					list[0] = "overwritten"
				}
			}
		})
		require.Equal(t, before, fmt.Sprintf("%#v", base), "%s: deriving changed the base", base.Name)
	}
}
