package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// dump is the IL this parse was written against, captured from ilrecon on 1.4.5.8.
func dump(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "prefix_il.txt"))
	require.NoError(t, err, "the captured IL is missing")
	return string(raw)
}

/*
TestTheToolReproducesTheCheckedInTable is the whole point of the tool: the table in
data/ is not transcribed, it is generated, and the generator must still produce exactly
what is committed -- byte for byte, so a regeneration after a game update shows only the
values that moved.
*/
func TestTheToolReproducesTheCheckedInTable(t *testing.T) {
	out, err := render(parse(dump(t)))
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("..", "..", "data", "prefix_stats.json"))
	require.NoError(t, err)
	require.Equal(t, string(want), string(out), "the generated table differs from data/prefix_stats.json")
}

/*
TestTheValuesAreTheGamesOwn pins a few entries as literals.

The table above is compared against a file this tool wrote, so on its own it would prove
only that the tool agrees with itself. These are read off the IL by hand at IL_00B7 and IL_05BB: prefix 81 (Legendary) is the
best a sword can roll, and prefix 36 (Demonic) only adds crit.
*/
func TestTheValuesAreTheGamesOwn(t *testing.T) {
	table := parse(dump(t))

	require.Equal(t, map[string]float64{
		"damage": 1.15, "knockback": 1.15, "crit": 5, "usetime": 0.9, "scale": 1.1,
	}, table[81], "Legendary")
	require.Equal(t, map[string]float64{"crit": 3}, table[36], "Demonic")
	// Seventy-eight, not the eighty-two modifiers the game has: the ones tested
	// with beq rather than bne share a block that writes through an
	// out-parameter this table does not carry, and four record nothing.
	require.Len(t, table, 78, "the modifiers that scale a field this table carries")
}

/*
TestADefaultIsNotRecorded: the method assigns 1 to every out-parameter before the chain,
and each block writes only what it changes. A parse that recorded those would bury the
real values under 82 rows of ones.
*/
func TestADefaultIsNotRecorded(t *testing.T) {
	for id, stats := range parse(dump(t)) {
		for field, v := range stats {
			switch field {
			case "crit", "tagdamage", "armorpen":
				require.NotZero(t, v, "prefix %d records a zero bonus for %s", id, field)
			default:
				require.NotEqual(t, 1.0, v, "prefix %d records a multiplier of 1 for %s", id, field)
			}
		}
	}
}

// TestAWholeNumberKeepsItsPoint: Go drops the fractional part and the game's file does
// not, which would otherwise rewrite every crit bonus on the next regeneration.
func TestAWholeNumberKeepsItsPoint(t *testing.T) {
	raw, err := json.Marshal(map[string]num{"crit": 3, "scale": 1.12})
	require.NoError(t, err)
	require.JSONEq(t, `{"crit":3.0,"scale":1.12}`, string(raw))
	require.Contains(t, string(raw), "3.0")
}

// TestAnEmptyDumpIsEmpty: a changed IL shape must read as nothing, which is what the
// tool's floor then refuses, rather than as a short table it would write over the good
// one.
func TestAnEmptyDumpIsEmpty(t *testing.T) {
	require.Empty(t, parse(""))
	require.Empty(t, parse("IL_0000: nop\nIL_0001: ret\n"))
}

/*
TestASharedBlockBelongsToTheOneItFallsInto pins what happens at IL_0802, where four
modifiers are tested with beq and share a block.

Only the bne test opens a block, so a shared block is recorded against the modifier the
chain falls through on -- not against the first of the four. That is a limitation, not a
reading of the game: it is harmless today only because the shared block writes through an
out-parameter this table does not carry. A future one that wrote a tracked field would
need the chain understood properly, and this test is what would have to change.

Loosening the branch test from `bne` to any `b` is an equivalent mutation: a block a beq
opens is empty either way, and an empty one is dropped. The narrower test is kept because
it says what the parse means, not because anything observable rests on it.
*/
func TestASharedBlockBelongsToTheOneItFallsInto(t *testing.T) {
	const il = `IL_0000: ldarg.1
IL_0001: ldc.i4.s 62
IL_0002: beq.s IL_0010
IL_0003: ldarg.1
IL_0004: ldc.i4.s 77
IL_0005: bne.un.s IL_0020
IL_0010: ldarg.2
IL_0011: ldc.r4 1.05
IL_0012: stind.r4
`
	require.Equal(t, map[int]map[string]float64{77: {"damage": 1.05}}, parse(il))
}
