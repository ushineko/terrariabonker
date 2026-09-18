package patch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
Arena slots are the same addresses in both, for every injection.

A slot's address is decided by an injection's position in an append-only list,
and an arena outlives the trainer process that made it -- so a slot that moves
between versions puts one run's stub on top of another run's live code. That has
happened, and it killed the game a few frames later somewhere unrelated.

Every name and every site index is compared, not a sample: the failure is
specific to whichever one moved.
*/
func TestEveryArenaSlotMatchesThePython(t *testing.T) {
	const arena = 0x20000000

	var want map[string]any
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import patcher as P
p = P.Patcher.__new__(P.Patcher)
p._arena = %d
p.arena = lambda *a, **k: %d
out = {"order": list(P._SLOT_ORDER), "slots": {}, "refused": []}
for name in P._SLOT_ORDER:
    out["slots"][name] = [p.slot_for(name, i) for i in range(P.Patcher.ARENA_MAX_SITES)]
for name, site in (("nonesuch", 0), ("loot", P.Patcher.ARENA_MAX_SITES), ("loot", -1)):
    try:
        p.slot_for(name, site)
        out["refused"].append(False)
    except P.PatchError:
        out["refused"].append(True)
print(json.dumps(out))`, arena, arena), &want)

	require.Equal(t, want["order"], asJSON(t, patch.SlotOrder), "a different slot order")

	slots := want["slots"].(map[string]any)
	for _, name := range patch.SlotOrder {
		addrs, known := slots[name]
		require.Truef(t, known, "%s has no slot in the Python", name)
		for i, w := range addrs.([]any) {
			got, err := patch.SlotFor(arena, name, i)
			require.NoErrorf(t, err, "%s site %d was refused", name, i)
			require.Equalf(t, uint32(w.(float64)), got, "%s site %d is somewhere else", name, i)
		}
	}

	// A name with no slot and a site index past the end are refused rather than
	// given an address that belongs to somebody else.
	for i, args := range [][2]any{{"nonesuch", 0}, {"loot", patch.ArenaMaxSites}, {"loot", -1}} {
		_, err := patch.SlotFor(arena, args[0].(string), args[1].(int))
		require.Equalf(t, want["refused"].([]any)[i], err != nil, "%v was judged differently", args)
	}
}

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

// The jump displacement is encoded the same way, in both directions.
func TestRel32MatchesThePython(t *testing.T) {
	cases := [][2]uint32{
		{0x10000005, 0x10000100}, // forwards
		{0x10000100, 0x10000005}, // backwards
		{0x10000005, 0x10000005}, // nowhere
		{0x00000005, 0xFFFFFF00}, // and a wrap, which two's complement makes right
	}
	var want []string
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import patcher as P
print(json.dumps([P.Patcher._rel32(a, b).hex() for a, b in %s]))`, pyPairs(cases)), &want)

	for i, c := range cases {
		require.Equalf(t, want[i], hexOf(patch.Rel32(c[0], c[1])),
			"a jump from %#x to %#x is encoded differently", c[0], c[1])
	}
}
