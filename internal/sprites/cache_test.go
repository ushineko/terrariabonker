package sprites_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
Where the icons go, and how a run knows whether it has anything to do.

The cache is disposable and rebuilt from the machine's own game files, so
nothing here is precious -- but a cache that reports itself finished when it is
not means wrong icons stay on screen until somebody bumps the scope, which is
the one failure worth guarding.
*/

// atHome puts the cache somewhere disposable and says where.
func atHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// The two implementations put the icons in the same place.
func TestTheCachePathsMatchThePython(t *testing.T) {
	var want struct {
		Dir    string `json:"dir"`
		Item   string `json:"item"`
		NPC    string `json:"npc"`
		Tinted string `json:"tinted"`
		Paths  string `json:"paths"`
		Scope  string `json:"scope"`
	}
	pyScript(t, `
from terrariabonker import sprites
print(json.dumps({"dir": sprites.cache_dir(), "item": sprites.icon_path(757),
                  "npc": sprites.npc_icon_path(1),
                  "tinted": sprites.npc_tinted_icon_path(-3),
                  "paths": sprites._PATHS_FILE, "scope": sprites._SCOPE}))`, &want)

	t.Setenv("HOME", realHome)
	require.Equal(t, want.Dir, sprites.CacheDir(""), "a different cache directory")
	require.Equal(t, want.Item, sprites.IconPath(757, ""), "a different item icon path")
	require.Equal(t, want.NPC, sprites.NPCIconPath(1, ""), "a different NPC icon path")
	require.Equal(t, want.Tinted, sprites.NPCTintedIconPath(-3, ""),
		"a different tinted icon path")
	require.Equal(t, want.Paths, sprites.PathsFile(), "a different remembered-paths file")
	require.Equal(t, want.Scope, sprites.Scope,
		"the two disagree about what a complete cache holds")
}

// An empty cache has nothing in it, and says so.
func TestAnEmptyCacheIsNotCached(t *testing.T) {
	atHome(t)
	require.False(t, sprites.IsCached(""), "an empty cache reported itself finished")
}

/*
A cache from an older scope reports itself unfinished.

Its icons may be wrong rather than merely fewer -- an un-cropped animation is a
column of little pictures -- so it has to be rebuilt rather than added to.
*/
func TestACacheFromAnOlderScopeIsNotCached(t *testing.T) {
	atHome(t)
	writeDone(t, sprites.Done{Version: version.KnownVersion, Scope: "all-v1", NPCs: 5})
	require.False(t, sprites.IsCached(""), "a cache from an older scope was trusted")
}

/*
And one written before the NPC frame counts were published reports the same.

That run skipped the NPC sheets, so the cache is real but incomplete; the next
run -- by which time the catalog has published the counts -- finishes the job.
*/
func TestACacheWithNoNPCsIsNotCached(t *testing.T) {
	atHome(t)
	writeDone(t, sprites.Done{Version: version.KnownVersion, Scope: sprites.Scope, NPCs: 0})
	require.False(t, sprites.IsCached(""), "a cache with no NPC icons was trusted")

	writeDone(t, sprites.Done{Version: version.KnownVersion, Scope: sprites.Scope, NPCs: 1})
	require.True(t, sprites.IsCached(""), "a complete cache was not recognised")
}

// A marker that is not a marker is ignored rather than trusted.
func TestAGarbageMarker(t *testing.T) {
	atHome(t)
	dir := sprites.CacheDir("")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".done"), []byte("{oh"), 0o600))
	require.False(t, sprites.IsCached(""), "a broken marker was read as a finished cache")
}

func writeDone(t *testing.T, done sprites.Done) {
	t.Helper()
	dir := sprites.CacheDir("")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	raw, err := json.Marshal(done)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".done"), raw, 0o600))
}

/*
The game's content directory is learned once from the running game and
remembered.

Nothing else can find it: Steam libraries move, and the game is the only thing
that knows where it was installed. Remembering it is what makes extraction work
with the game closed, which is when people actually run it.
*/
func TestTheContentDirectoryIsLearnedAndRemembered(t *testing.T) {
	home := atHome(t)
	require.Empty(t, sprites.ContentImagesDir(""),
		"a content directory was found with nothing to find it from")

	// A game installed somewhere, with the layout Steam gives it.
	install := filepath.Join(home, "SteamLibrary", "common", "Terraria")
	content := filepath.Join(install, "Content", "Images")
	require.NoError(t, os.MkdirAll(content, 0o755))
	exe := filepath.Join(install, "Terraria.exe")

	require.Equal(t, content, sprites.ContentImagesDir(exe), "the path was not derived")
	require.Equal(t, content, sprites.ContentImagesDir(""),
		"the path was not remembered for a run with the game closed")
}

// A remembered path that is no longer there is not used.
func TestARememberedPathThatHasGone(t *testing.T) {
	home := atHome(t)
	gone := filepath.Join(home, "gone", "Content", "Images")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprites.PathsFile()), 0o755))
	require.NoError(t, os.WriteFile(sprites.PathsFile(),
		[]byte(`{"content_images": `+quote(gone)+`}`), 0o600))

	require.Empty(t, sprites.ContentImagesDir(""),
		"a directory that is not there was handed back")
}

// An executable with no content beside it teaches nothing.
func TestAnExecutableWithNoContent(t *testing.T) {
	home := atHome(t)
	require.Empty(t, sprites.ContentImagesDir(filepath.Join(home, "elsewhere", "Terraria.exe")),
		"a content directory was invented")
	_, err := os.Stat(sprites.PathsFile())
	require.ErrorIs(t, err, os.ErrNotExist, "a path was remembered for a directory that is not there")
}
