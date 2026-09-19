package patch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

// Slots never overlap each other, whatever the list says.
func TestArenaSlotsDoNotOverlap(t *testing.T) {
	const arena = 0x20000000
	seen := map[uint32]string{}
	for _, name := range patch.SlotOrder {
		for site := range patch.ArenaMaxSites {
			addr, err := patch.SlotFor(arena, name, site)
			require.NoError(t, err)
			for off := range uint32(patch.ArenaSlot) {
				where := fmt.Sprintf("%s[%d]", name, site)
				prev, clash := seen[addr+off]
				require.Falsef(t, clash, "%s shares %#x with %s", where, addr+off, prev)
				seen[addr+off] = where
			}
		}
		_ = name
	}
	// And the whole table fits in front of the stamp.
	last, err := patch.SlotFor(arena, patch.SlotOrder[len(patch.SlotOrder)-1], patch.ArenaMaxSites-1)
	require.NoError(t, err)
	require.LessOrEqual(t, last+patch.ArenaSlot, uint32(arena+patch.ArenaMagicOff),
		"the last slot runs into the arena stamp")
}
