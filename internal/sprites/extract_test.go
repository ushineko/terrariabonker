package sprites_test

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
Building the cache out of the game's own files.

The pieces are compared on their own above; this is the whole thing, against
the same game files, with the same frame counts -- because the interesting
failures are in the joins. An item with no sprite of its own falls through to
the tile sheet; an NPC is cropped with a count that comes from a file the other
side wrote; a tinted variant is painted from the type's icon, which has to exist
by then.
*/

// gameContent is where the game's sprites are, or the test is skipped.
func gameContent(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(realHome, ".cache", "terrariabonker", "paths.json"))
	if err != nil {
		t.Skip("the game's content directory has not been learned on this machine")
	}
	var paths struct {
		ContentImages string `json:"content_images"`
	}
	if err := json.Unmarshal(raw, &paths); err != nil || paths.ContentImages == "" {
		t.Skip("the game's content directory has not been learned on this machine")
	}
	if _, err := os.Stat(paths.ContentImages); err != nil {
		t.Skip("the game's content directory is not there")
	}
	return paths.ContentImages
}

/*
gameFiles points the cache at a scratch directory that already knows where the
game is.

The learned path is what makes extraction work with the game closed, and a test
that moves HOME loses it -- so it is copied across rather than re-derived, which
would need the game running.
*/
func gameFiles(t *testing.T) string {
	t.Helper()
	content := gameContent(t)
	atHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(sprites.PathsFile()), 0o755))
	require.NoError(t, os.WriteFile(sprites.PathsFile(),
		[]byte(`{"content_images": `+quote(content)+`}`), 0o600))
	return content
}

/*
someItems is a spread of the catalog, chosen for the paths through the
extractor rather than for being interesting items.

3210 is a trapped chest: it has no sprite file of its own and is drawn from the
tile sheet instead, which is the one item kind that would otherwise be a blank
square in the recipe book.
*/
var someItems = []int{1, 8, 9, 24, 29, 75, 149, 757, 2294, 2676, 3210, 3509, 5000}

// someNPCs are the frame counts the extractor is given, including the two-frame
// slime the tints hang off.
var someNPCs = map[int32]int32{1: 2, 4: 15, 16: 2}

// someTints is the colour a variant is painted with, keyed by netID as the game
// keys it.
var someTints = map[int32]sprites.NPCTint{
	-3: {Type: 1, Color: [4]byte{102, 204, 106, 255}},
	1:  {Type: 1, Color: [4]byte{0, 80, 255, 100}},
}

/*
An extraction produces the same icons as the implementation it replaces.

Compared file by file rather than by count: two extractions that both wrote
nothing agree perfectly, and two that cropped differently produce the same
number of files.
*/
func TestExtractingMatchesThePython(t *testing.T) {
	content := gameFiles(t)
	plantDrawData(t)

	got, err := sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.NoError(t, err)
	require.Positive(t, got.OK, "nothing was extracted at all")

	pyDir := filepath.Join(t.TempDir(), "python")
	pyExtract(t, content, pyDir)

	mine := sprites.CacheDir("")
	names := iconsIn(t, mine)
	require.Equal(t, iconsIn(t, filepath.Join(pyDir, version.KnownVersion)), names,
		"the two wrote different sets of icons")
	require.NotEmpty(t, names)

	for _, name := range names {
		require.Equalf(t, pixels(t, filepath.Join(pyDir, version.KnownVersion, name)),
			pixels(t, filepath.Join(mine, name)), "%s came out differently", name)
	}

	// And the paths this promises are the paths it wrote.
	require.FileExists(t, sprites.IconPath(757, ""), "the Zenith's icon is not where it says")
	require.FileExists(t, sprites.NPCIconPath(1, ""), "the slime's icon is not where it says")
	require.FileExists(t, sprites.NPCTintedIconPath(-3, ""),
		"the tinted variant's icon is not where it says")
}

/*
A second run leaves the cache alone, and --force rebuilds it.

Extraction is minutes of work over fourteen thousand files, so a run that
redecoded everything would make the window's "extract icons" button unusable.
*/
func TestASecondExtractionIsCheap(t *testing.T) {
	gameFiles(t)
	plantDrawData(t)

	_, err := sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.NoError(t, err)
	require.True(t, sprites.IsCached(""), "a finished extraction did not say so")

	icon := sprites.IconPath(757, "")
	before := stat(t, icon)

	_, err = sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.NoError(t, err)
	require.Equal(t, before, stat(t, icon), "a cached icon was decoded again")

	_, err = sprites.Extract(sprites.Options{ItemIDs: someItems, Force: true})
	require.NoError(t, err)
	require.NotEqual(t, before, stat(t, icon), "--force did not rebuild the icon")
}

/*
Without the frame counts the NPC sheets are skipped rather than cached whole.

A wrong icon persists until somebody bumps the scope; a missing one is fixed by
the next run, which is why the marker also reports the cache unfinished.
*/
func TestNPCsAreSkippedWithoutFrameCounts(t *testing.T) {
	gameFiles(t)

	got, err := sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.NoError(t, err)
	require.Positive(t, got.OK, "no items were extracted either")
	require.NoFileExists(t, sprites.NPCIconPath(1, ""),
		"an NPC sheet was cached with no way to crop it")
	require.False(t, sprites.IsCached(""),
		"a cache with no NPC icons reported itself finished")
}

// With no game files there is nothing to do, and it says which.
func TestExtractingWithNoGameFiles(t *testing.T) {
	atHome(t)
	_, err := sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.ErrorIs(t, err, sprites.ErrNoContent)
}

// plantDrawData writes the frame counts and tints the privileged side would
// have written.
func plantDrawData(t *testing.T) {
	t.Helper()
	require.NoError(t, sprites.SaveNPCDrawData(someNPCs, someTints))
}

// pyExtract runs the other implementation over the same files, into its own
// directory.
func pyExtract(t *testing.T, content, into string) {
	t.Helper()
	items, err := json.Marshal(someItems)
	require.NoError(t, err)
	pyScript(t, `
from terrariabonker import sprites
sprites._CACHE_ROOT = `+quote(into)+`
sprites._PATHS_FILE = `+quote(filepath.Join(into, "paths.json"))+`
sprites._NPC_FRAMES_FILE = `+quote(sprites.NPCFramesFile())+`
sprites._save_content_dir(`+quote(content)+`)
ok, failed, total = sprites.extract(item_ids=`+string(items)+`)
print(json.dumps({"ok": ok, "failed": failed, "total": total}))`, &struct {
		OK     int `json:"ok"`
		Failed int `json:"failed"`
		Total  int `json:"total"`
	}{})
}

// iconsIn is the PNGs in a directory, in order.
func iconsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			out = append(out, e.Name())
		}
	}
	return out
}

/*
pixels is one icon's picture, for comparing two of them.

Decoded rather than compared as files: the two write PNGs with different
encoders, so identical pictures are different bytes, and comparing the bytes
would report every icon as wrong.
*/
func pixels(t *testing.T, path string) shape {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // a path this test made
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	img, err := png.Decode(f)
	require.NoError(t, err)
	return describe(t, img)
}

// stat is when a file was last written, which is how a skipped decode is told
// from a repeated one.
func stat(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.ModTime().String() + itoa(int(info.Size()))
}
