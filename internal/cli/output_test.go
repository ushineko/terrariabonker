package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
What a command prints, which is the only part of it anybody sees.

The operations underneath are covered where they live. What is checked here is
the wiring: that --json produces the shape the window decodes, that the text
form says what happened, and that a failure comes back as a failure rather than
as a cheerful empty line.
*/

// A status reports the player it found.
func TestStatusPrintsThePlayer(t *testing.T) {
	out := ranOK(t, plantGame(), "status")
	require.Contains(t, out, "Terraria PID", "the process was not reported")
	require.Contains(t, out, `"Nakama": HP 137/500  Mana 200/220`,
		"the player's own numbers were not reported")
	require.Contains(t, out, "slot  0: type=3509", "the inventory was not listed")
	require.NotContains(t, out, "slot  1:", "an empty slot was listed")
}

// And --json is the shape the window decodes.
func TestStatusJSONIsWhatTheWindowReads(t *testing.T) {
	out := ranOK(t, plantGame(), "status", "--json")

	var got struct {
		PID     int     `json:"pid"`
		Copies  int     `json:"copies"`
		Name    *string `json:"name"`
		HP      *int    `json:"hp"`
		MaxHP   *int    `json:"max_hp"`
		Mana    *int    `json:"mana"`
		MaxMana *int    `json:"max_mana"`
		Level   string  `json:"compat_level"`
		World   any     `json:"world"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "the reply is not JSON")
	require.NotNil(t, got.Name)
	require.Equal(t, "Nakama", *got.Name)
	require.Equal(t, 137, *got.HP)
	require.Equal(t, 500, *got.MaxHP)
	require.Equal(t, 220, *got.MaxMana)
	require.Positive(t, got.Copies, "no player copies were reported")
	require.NotEmpty(t, got.Level, "the compatibility level is a word, and it is missing")
	require.Nil(t, got.World, "a world was reported for a game with none loaded")
}

/*
The status a fresh window parses is the one this emits.

The window's own decoder is the other half of the contract, and it has already
rejected a whole reply once over a field that was a number here and a word
there -- which cost the status bar, the build gate and auto-restore at once,
because all three only run on a status that parsed.
*/
func TestTheWindowCanDecodeTheStatus(t *testing.T) {
	out := ranOK(t, plantGame(), "status", "--json")
	status, err := clientStatus(out)
	require.NoError(t, err, "the window could not decode the status")
	require.NotNil(t, status.HP)
	require.Equal(t, 137, *status.HP)
	require.NotNil(t, status.Name, "the window sees no player name")
}

// An empty inventory listing still says how many slots there are.
func TestInventoryListsWhatIsCarried(t *testing.T) {
	mem := plantGame()
	out := ranOK(t, mem, "inventory")
	require.Contains(t, out, " slots")
	require.Contains(t, out, "type=3509")
	require.Contains(t, out, "useTime=20 pick=210", "a tool's numbers were not shown")
	require.NotContains(t, out, "type=0", "an empty slot was listed")

	all := ranOK(t, mem, "inventory", "--all")
	require.Contains(t, all, "type=0", "--all did not list the empty slots")
}

// The inventory the window reads is a list of slots, not an object.
func TestInventoryJSONIsAList(t *testing.T) {
	out := ranOK(t, plantGame(), "inventory", "--all", "--json")
	var slots []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &slots))
	require.NotEmpty(t, slots)
	require.Contains(t, slots[0], "auto_reuse", "a field the window reads is missing")
}

/*
A build that is not the known-good one is reported, and says so in the exit
code.

Not a failure: the report ran and its answer is "no". A script wants to tell
that from the command not working.
*/
func TestVersionReportsAnUnknownBuild(t *testing.T) {
	code, out, _ := run(t, plantGame(), "version")
	require.Equal(t, 2, code, "an unknown build was reported as fine")
	require.Contains(t, out, "detected version :")
	require.Contains(t, out, "compatibility    :")
}

/*
Writing to a build nobody has checked is refused, and --force is the override.

The gate is on writing rather than on reading: a read against a build whose
offsets moved finds no player, while a write puts numbers into the wrong fields
of a live save.
*/
func TestWritingToAnIncompatibleBuildIsRefused(t *testing.T) {
	mem := plantGame()
	plantVersionInto(mem, "1.0.0.0")

	code, _, stderr := run(t, mem, "set-hp", "300")
	require.Equal(t, cli.ExitFailure, code, "a write to an incompatible build was allowed")
	require.Contains(t, stderr, "[ERROR]", "the refusal was not reported as one")

	out := ranOK(t, mem, "set-hp", "300", "--force")
	require.Contains(t, out, "[OK] set HP to 300")
}

// A build nobody has said anything about is written to, because "unknown" is
// not "wrong": the offsets may well still fit, and refusing every new hotfix
// would make the trainer useless the day the game updates.
func TestWritingToAnUnknownBuildIsAllowed(t *testing.T) {
	out := ranOK(t, plantGame(), "set-hp", "300")
	require.Contains(t, out, "[OK] set HP to 300")
}

// The catalogs need no game at all, which is the whole point of them.
func TestTheCatalogsNeedNoGame(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		has  string
	}{
		{[]string{"patch", "catalog", "--json"}, "mining"},
		{[]string{"names"}, "Terra Blade"},
		{[]string{"prefixes"}, "Legendary"},
		{[]string{"recipes"}, "recipes"},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			var stdout, stderr strings.Builder
			app := cli.NewApp(cli.Options{
				Elevate: func() error { return nil },
				Attach: func() (*cli.Game, error) {
					require.Fail(t, "a catalog attached to the game")
					return nil, nil
				},
			})
			code := app.Execute(t.Context(), tc.argv, &stdout, &stderr)
			require.Equalf(t, cli.ExitOK, code, "failed: %s", stderr.String())
			require.Contains(t, stdout.String(), tc.has)
			require.True(t, json.Valid([]byte(stdout.String())), "a catalog is not JSON")
		})
	}
}

// The patch catalog says everything the window needs to build its controls.
func TestThePatchCatalogDescribesEveryCheat(t *testing.T) {
	var stdout, stderr strings.Builder
	app := cli.NewApp(cli.Options{Elevate: func() error { return nil }})
	require.Equal(t, cli.ExitOK,
		app.Execute(t.Context(), []string{"patch", "catalog", "--json"}, &stdout, &stderr))

	var catalog []struct {
		Name    string `json:"name"`
		Label   string `json:"label"`
		Section string `json:"section"`
		Kind    string `json:"kind"`
		Value   *struct {
			Kind    string  `json:"kind"`
			Default float64 `json:"default"`
			Lo      float64 `json:"lo"`
			Hi      float64 `json:"hi"`
		} `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &catalog))
	require.Len(t, catalog, len(patch.Catalog()), "a cheat is missing from the catalog")

	tuned := 0
	for _, entry := range catalog {
		require.NotEmptyf(t, entry.Label, "%s has no label", entry.Name)
		require.NotEmptyf(t, entry.Section, "%s is in no section", entry.Name)
		require.NotEmptyf(t, entry.Kind, "%s has no kind", entry.Name)
		if entry.Value != nil {
			tuned++
			require.Containsf(t, []string{"f32", "i32"}, entry.Value.Kind,
				"%s has a value of no known kind", entry.Name)
			require.Lessf(t, entry.Value.Lo, entry.Value.Hi,
				"%s has an empty range", entry.Name)
		}
	}
	require.Equal(t, len(patch.ValueSpecs), tuned, "a tunable lost its range")
}

// An unknown subcommand is a misuse, which is its own exit code.
func TestAnUnknownCommandIsAMisuse(t *testing.T) {
	code, _, stderr := run(t, plantGame(), "not-a-command")
	require.Equal(t, cli.ExitUsage, code, "a typo was not reported as a misuse")
	require.Contains(t, stderr, "not-a-command")
}

// And so is a flag that does not exist.
func TestAnUnknownFlagIsAMisuse(t *testing.T) {
	code, _, _ := run(t, plantGame(), "status", "--nonsense")
	require.Equal(t, cli.ExitUsage, code, "an unknown flag was not reported as a misuse")
}

/*
A bare invocation reports what is running.

It used to launch the window, and the window is a separate program now -- so the
command line is a command line, and typing the name alone answers the question
people were opening it to ask.
*/
func TestABareInvocationReportsStatus(t *testing.T) {
	out := ranOK(t, plantGame())
	require.Contains(t, out, "Terraria PID")
}

// clientStatus decodes a status the way the window does.
func clientStatus(out string) (statusReader, error) {
	var got statusReader
	err := json.Unmarshal([]byte(out), &got)
	return got, err
}

// statusReader is the window's own view of a status, kept here so this test
// fails when the two stop agreeing.
type statusReader struct {
	Version string  `json:"version"`
	BuildID string  `json:"buildid"`
	HP      *int    `json:"hp"`
	Name    *string `json:"name"`
}

// BuildKey is the version and build id, which is what the gate keys on.
func (s statusReader) BuildKey() string {
	if s.BuildID == "" {
		return s.Version
	}
	return s.Version + "+" + s.BuildID
}

var _ = game.ItemNames // the catalogs above read the bundled tables

/*
A status says which world is loaded.

The panel re-applies the saved profile on a world switch and not only on a new
process, so a status that never names the world means the cheats quietly stop
being re-applied when somebody changes map.
*/
func TestStatusNamesTheLoadedWorld(t *testing.T) {
	mem := plantGame()
	plantWorldInto(mem, "Nakama's World")

	var got struct {
		World []any `json:"world"`
	}
	require.NoError(t, json.Unmarshal([]byte(ranOK(t, mem, "status", "--json")), &got))
	require.NotEmpty(t, got.World, "the loaded world was not reported")
	require.Contains(t, got.World, "Nakama's World", "a different world was reported")
}
