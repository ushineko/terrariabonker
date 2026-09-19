package sprites_test

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
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
An extraction produces exactly these icons, pixel for pixel, out of the game's
own files.

Every one is named and every one carries its size and its pixels, because the
interesting failures are in the joins: 3210 is a trapped chest with no sprite
file of its own and is drawn from the tile sheet; the NPC sheets are cropped
with a count that comes from a file the privileged side wrote; and the tinted
variants are painted from the type's icon, which has to exist by then.

These were agreed with the implementation this was ported from, over the same
game files.
*/
func TestExtractingTheGamesIcons(t *testing.T) {
	gameFiles(t)
	plantDrawData(t)

	got, err := sprites.Extract(sprites.Options{ItemIDs: someItems})
	require.NoError(t, err)
	require.Positive(t, got.OK, "nothing was extracted at all")

	want := map[string]shape{
		"Item_1.png":    {"32x32", "f9bda977d5d7a7a3"},
		"Item_8.png":    {"14x16", "9380f2d7df5a2f12"},
		"Item_9.png":    {"24x22", "3f1a4e893a6a2569"},
		"Item_24.png":   {"32x32", "11ecdb365e349724"},
		"Item_29.png":   {"22x22", "05ba35eadf57eb27"},
		"Item_75.png":   {"22x26", "138efc12100672f7"},
		"Item_149.png":  {"28x30", "d033010f0485e618"},
		"Item_757.png":  {"46x54", "92a3c5cf7f3a3fee"},
		"Item_2294.png": {"48x48", "60d44c91d00fa468"},
		"Item_2676.png": {"24x24", "81fc65175d485d98"},
		// The trapped chest, drawn from the tile sheet because it has no
		// sprite of its own.
		"Item_3210.png": {"34x32", "ae82064d9cfd5529"},
		"Item_3509.png": {"32x32", "a758d292cd126f09"},
		"Item_5000.png": {"34x32", "6ca4bc3183f807da"},
		// The NPC sheets, cropped with the counts the privileged side published.
		"NPC_1.png":  {"32x26", "6bd0ada19ec41a1a"},
		"NPC_4.png":  {"110x66", "67d93c9a0fc91634"},
		"NPC_16.png": {"44x34", "8cfa1c932a54a625"},
		// And the two variants, painted from the slime's own icon.
		"NPCt_-3.png": {"32x26", "f7827b99c02690f0"},
		"NPCt_1.png":  {"32x26", "b7845b36d68c8b52"},
	}

	dir := sprites.CacheDir("")
	names := iconsIn(t, dir)
	require.Len(t, names, len(want), "a different set of icons was written")
	for _, name := range names {
		expected, known := want[name]
		require.Truef(t, known, "%s was written and is not one of these", name)
		require.Equalf(t, expected, pixels(t, filepath.Join(dir, name)),
			"%s came out differently", name)
	}

	// And the paths this promises are the paths it wrote.
	require.FileExists(t, sprites.IconPath(757, ""), "the Terra Blade's icon is not where it says")
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
