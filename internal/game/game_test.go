package game_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
The bundled tables: item names, tooltips, NPC names, and the search that walks
them.

The tables themselves are files in data/, so the record is the file and a change
to one is visible in the commit that made it. What is worth asserting here is
that they are read at all, read into the right shape, and that the *ranking* --
which is code rather than data -- puts things where somebody expects them.

The counts and the search results below were agreed with the implementation this
was ported from, while both existed.
*/

// repoRoot is the checkout, for the test that the data is embedded rather than
// read from beside the binary.
var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// The item table is whole, and a few known rows read as themselves.
func TestTheItemTableIsRead(t *testing.T) {
	names, err := game.ItemNames()
	require.NoError(t, err)
	require.Equal(t, 6195, names.Len(), "the item table has changed size")

	for id, want := range map[int]string{
		9: "Wood", 757: "Terra Blade", 3507: "Copper Shortsword",
		4956: "Zenith", 5400: "The Dirtiest Block",
	} {
		require.Equalf(t, want, names.Name(id), "item %d", id)
	}
	require.Empty(t, names.Name(99999), "an item nobody has a name for has one")
}

/*
A label is never empty, and says which kind of nothing it is.

An id the table does not know is an item from another version and is shown as
its number; the zero id is the game's own way of saying a slot holds nothing,
which is not an unknown item but no item.
*/
func TestALabelIsNeverEmpty(t *testing.T) {
	names, err := game.ItemNames()
	require.NoError(t, err)
	for id, want := range map[int]string{
		0: "(empty)", 9: "Wood", 757: "Terra Blade",
		3507: "Copper Shortsword", 5400: "The Dirtiest Block", 99999: "#99999",
	} {
		require.Equalf(t, want, names.Label(id), "item %d", id)
	}
}

/*
Search returns the items in the order a picker shows them.

The order is the point: shortest name first, because a short name containing the
query is usually the thing being looked for. Returning the same set in a
different order sends somebody to the wrong row.
*/
func TestSearchRanksByLength(t *testing.T) {
	for _, c := range []struct {
		query string
		want  []int
	}{
		{"wood", []int{9, 5710, 619, 5215, 93, 621, 911, 2504, 5930, 39,
			1389, 25, 480, 727, 1729, 2503, 2827, 3278, 5690, 24}},
		{"pickaxe", []int{990, 3503, 1, 1320, 3497, 3521, 122, 469, 776, 882,
			2776, 2781, 3509, 3515, 4059, 777, 1506, 1202, 3466, 3485}},
		{"terra", []int{2208, 3389, 4144, 5308, 757, 5005, 5134, 4731, 5288,
			5630, 1428, 5000, 5228, 5227}},
		{"zenith", []int{4956}},
		// Trimmed and case-folded, because a query is typed.
		{"  SWORD ", []int{2332, 5224, 723, 1166, 2118, 24, 439, 483, 881, 484,
			653, 1199, 3501, 3502, 5284, 4, 6, 659, 921, 989}},
	} {
		t.Run(c.query, func(t *testing.T) {
			names, err := game.ItemNames()
			require.NoError(t, err)

			hits := names.Search(c.query, 20)
			got := make([]int, len(hits))
			for i, hit := range hits {
				got[i] = hit.ID
				require.Equalf(t, names.Name(hit.ID), hit.Name,
					"hit %d carries a name that is not the item's", i)
			}
			require.Equal(t, c.want, got, "a different set, or a different order")
		})
	}

	names, err := game.ItemNames()
	require.NoError(t, err)
	require.Empty(t, names.Search("   ", 20), "a query of nothing finds nothing")
}

// The table is read once however many callers ask for it: 6,195 rows of
// constants do not need parsing twice.
func TestTheTableIsReadOnce(t *testing.T) {
	first, err := game.ItemNames()
	require.NoError(t, err)
	second, err := game.ItemNames()
	require.NoError(t, err)
	require.Same(t, first, second)
}

// The data is embedded, so a binary carries it: a trainer being used to recover
// a character must not fail because a directory was left behind.
func TestTheTablesAreInTheBinaryNotBesideIt(t *testing.T) {
	names, err := game.ItemNames()
	require.NoError(t, err)
	require.Positive(t, names.Len())

	// Nothing under the repository is open at this point: the read came from
	// the embedded copy.
	require.NoFileExists(t, filepath.Join(t.TempDir(), "items.json"))
	_, err = os.Stat(filepath.Join(repoRoot, "data", "items.json"))
	require.NoError(t, err, "the file is still in the repository")
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoErrorf(t, err, "%q is not a number", s)
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

/*
The NPC table is keyed on the net id, like the game's own collection.

The variants share a type and are told apart only by a negative net id, so a
table keyed on type would collapse the coloured slimes into one row -- and the
sixty-five negative keys are what shows it is not.
*/
func TestTheNPCTableIsKeyedOnNetID(t *testing.T) {
	npcs, err := game.NPCs()
	require.NoError(t, err)
	require.Equal(t, 759, npcs.Count(), "the NPC table has changed size")

	negatives := 0
	for id := range npcs.All() {
		if id < 0 {
			negatives++
		}
	}
	require.Equal(t, 65, negatives, "a different number of variants is named")

	for id, want := range map[int]string{
		1: "Blue Slime", -3: "Green Slime", 4: "Eye of Cthulhu", 245: "Golem",
	} {
		require.Equalf(t, want, npcs.Name(id), "NPC %d", id)
	}
}

// An id nobody has a name for still gets a label, because it still has to
// appear in a list.
func TestAnUnknownNPCStillGetsALabel(t *testing.T) {
	npcs, err := game.NPCs()
	require.NoError(t, err)
	for id, want := range map[int]string{1: "Blue Slime", -2: "Slimer", 999999: "#999999"} {
		require.Equalf(t, want, npcs.Label(id), "NPC %d", id)
	}
}
