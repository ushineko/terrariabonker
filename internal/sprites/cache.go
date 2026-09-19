package sprites

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ushineko/terrariabonker/internal/version"
)

/*
Where the icons live, and how the extractor knows whether it has been run.

Everything here is unprivileged: it reads the game's Content/Images from disk
and the running game's mapped path, never its memory. The cache is disposable
and lives under ~/.cache -- reconstitutable on any machine from that machine's
own game files, which is why no sprites are committed.

Under ~/.cache rather than ~/.config on purpose: extraction runs unprivileged
and the config directory can end up root-owned from a sudo memory command,
which would make this unwritable.
*/

/*
Scope names the set of icons a cache holds.

Bumped when that set changes, so a cache from an older scope re-extracts rather
than being trusted: all-v1 was every named item with animated strips reduced to
their first frame, v2 added tile-sheet icons for placeable items with no sprite
of their own, v3 added NPC sheets cropped with the game's own frame counts, and
v4 crops the grid sheets to their top-left cell.
*/
const Scope = "all-v4"

// CacheRoot is where every version's icons go.
func CacheRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "terrariabonker", "sprites")
	}
	return filepath.Join(home, ".cache", "terrariabonker", "sprites")
}

// CacheDir is one game version's icons.
func CacheDir(v string) string {
	if v == "" {
		v = version.KnownVersion
	}
	return filepath.Join(CacheRoot(), v)
}

// IconPath is one item's icon.
func IconPath(itemID int, v string) string {
	return filepath.Join(CacheDir(v), fmt.Sprintf("Item_%d.png", itemID))
}

// NPCIconPath is one NPC type's icon. NPCs share the cache but not the
// namespace -- their ids collide with items'.
func NPCIconPath(npcType int32, v string) string {
	return filepath.Join(CacheDir(v), fmt.Sprintf("NPC_%d.png", npcType))
}

/*
NPCTintedIconPath is a tinted variant's own icon.

The sheet is per type but the tint is per netID -- every coloured slime shares
one neutral sheet and differs only by the colour -- so a tinted variant cannot
reuse the type's icon.
*/
func NPCTintedIconPath(netID int32, v string) string {
	return filepath.Join(CacheDir(v), fmt.Sprintf("NPCt_%d.png", netID))
}

// Done is the marker an extraction leaves behind.
type Done struct {
	Version string `json:"version"`
	Scope   string `json:"scope"`
	OK      int    `json:"ok"`
	Failed  int    `json:"failed"`
	Total   int    `json:"total"`
	NPCs    int    `json:"npcs"`
	Tinted  int    `json:"tinted"`
}

/*
IsCached reports whether an extraction for this version and scope has finished.

An extraction that ran before the frame counts were published also reports
false: it skipped the NPC sheets, so the cache is real but incomplete, and the
next run -- by which time the catalog has published the counts -- finishes the
job.
*/
func IsCached(v string) bool {
	raw, err := os.ReadFile(filepath.Join(CacheDir(v), ".done")) //nolint:gosec // a path this package built
	if err != nil {
		return false
	}
	var done Done
	if err := json.Unmarshal(raw, &done); err != nil {
		return false
	}
	return done.Scope == Scope && done.NPCs > 0
}

// PathsFile is where the learned content directory is remembered.
func PathsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "terrariabonker", "paths.json")
	}
	return filepath.Join(home, ".cache", "terrariabonker", "paths.json")
}

/*
ContentImagesDir is the game's Content/Images, or nothing.

A path learned earlier is preferred, so extraction works with the game closed;
otherwise it is derived from the running game's own mapped executable and
remembered. Nothing else can find it: Steam libraries move, and the game is the
only thing that knows where it was installed.
*/
func ContentImagesDir(exePath string) string {
	if learned := loadPaths()["content_images"]; learned != "" && isDir(learned) {
		return learned
	}
	if exePath == "" {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exePath), "Content", "Images")
	if !isDir(candidate) {
		return ""
	}
	savePath("content_images", candidate)
	return candidate
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func loadPaths() map[string]string {
	raw, err := os.ReadFile(PathsFile()) //nolint:gosec // a path this package built
	if err != nil {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// savePath remembers one learned path. Best effort: a path that cannot be
// written is one that is learned again next time.
func savePath(key, value string) {
	path := PathsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	all := loadPaths()
	all[key] = value
	raw, err := json.Marshal(all)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644) //nolint:gosec // the user's own cache
}
