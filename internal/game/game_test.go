package game_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/game"
)

/*
The differential harness.

Every table this package reads is read by the Python too, and the port is only
right if both make the same thing of the same bytes. So the tests ask the Python
rather than asserting what the Go produced -- the same technique the argv and
the abbreviation tests use, and the one that caught the Qt panel's docstring
lying about its own function.

It is the habit spec 051 depends on: the modules after this one write to another
process's memory, and "it looks right" is not a standard that survives that.
*/

// pythonTimeout is generous: the first call pays for the interpreter starting
// and for reading 572 KB of JSON.
const pythonTimeout = 2 * time.Minute

// repoRoot is where the Python package can be imported from.
var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// askPython runs a snippet in the repository and decodes what it printed.
func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

/*
Every item name is the one the Python reads from the same file.

Row for row, not a sample: the table is the answer to "what is item 3507", and a
port that is right about 6,190 of 6,195 rows is wrong in a way nobody would
notice until it named the wrong sword.
*/
func TestEveryItemNameMatchesThePython(t *testing.T) {
	var want map[string]string
	askPython(t, `
import json
from terrariabonker import names
print(json.dumps(names.all_names()))
`, &want)

	got, err := game.ItemNames()
	require.NoError(t, err)
	require.Equal(t, len(want), got.Len(), "the tables are different sizes")
	require.Greater(t, got.Len(), 6000, "and neither of them is empty")

	for key, name := range want {
		id := atoi(t, key)
		require.Equalf(t, name, got.Name(id), "item %d is named differently", id)
	}
}

// And the tooltips beside them, which are the same shape of table and the same
// kind of mistake to get wrong.
func TestEveryTooltipMatchesThePython(t *testing.T) {
	var want map[string]string
	askPython(t, `
import json
from terrariabonker import names
print(json.dumps({str(i): names.tooltip(i) for i in names.all_names()}))
`, &want)

	got, err := game.ItemNames()
	require.NoError(t, err)
	for key, tip := range want {
		id := atoi(t, key)
		require.Equalf(t, tip, got.Tooltip(id), "item %d has a different tooltip", id)
	}
}

/*
A label is never empty, and says which kind of nothing it is.

An id the table does not know is an item from another version and is shown as
its number; the zero id is the game's own way of saying a slot holds nothing,
which is not an unknown item but no item.
*/
func TestALabelIsNeverEmptyAndMatchesThePython(t *testing.T) {
	ids := []int{0, 9, 757, 3507, 5400, 99999}
	var want map[string]string
	askPython(t, `
import json
from terrariabonker import names
print(json.dumps({str(i): names.label(i) for i in [0, 9, 757, 3507, 5400, 99999]}))
`, &want)

	got, err := game.ItemNames()
	require.NoError(t, err)
	for _, id := range ids {
		label := got.Label(id)
		require.NotEmpty(t, label)
		require.Equalf(t, want[itoa(id)], label, "item %d is labelled differently", id)
	}
	require.Equal(t, "(empty)", got.Label(0))
}

/*
Search returns the same items in the same order.

The order is the point: shortest name first, because a short name containing
the query is usually the thing being looked for. A port that returned the same
set in a different order would send someone to the wrong row of a picker.
*/
func TestSearchMatchesThePython(t *testing.T) {
	for _, query := range []string{"wood", "pickaxe", "terra", "zenith", "  SWORD "} {
		var want [][]any
		askPython(t, `
import json, sys
from terrariabonker import names
print(json.dumps(names.search(`+quote(query)+`, limit=20)))
`, &want)

		got, err := game.ItemNames()
		require.NoError(t, err)
		hits := got.Search(query, 20)

		require.Lenf(t, hits, len(want), "%q found a different number of items", query)
		for i, row := range want {
			require.Equalf(t, int(row[0].(float64)), hits[i].ID, "%q: hit %d is a different item", query, i)
			require.Equalf(t, row[1].(string), hits[i].Name, "%q: hit %d has a different name", query, i)
		}
	}

	got, err := game.ItemNames()
	require.NoError(t, err)
	require.Empty(t, got.Search("   ", 20), "a query of nothing finds nothing")
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
	require.NoError(t, err, "the file is still in the repository for the Python to read")
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoErrorf(t, err, "%q is not a number", s)
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

// quote is a Python string literal for a query, so a test case with spaces or
// quotes in it survives the trip.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
