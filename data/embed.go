/*
Package data is the game's own tables, as they were extracted from it.

Seven JSON files, 572 KB, and not one line of them was written by hand: item
names and tooltips come out of Terraria.exe's ItemID fields joined to its
embedded localization resource, the recipes and NPCs out of the same binary, the
prefix stats out of its IL. `tools/` holds the extractors, and `docs/discovery.md`
says how each was found.

They are data rather than code, so the port to Go does not rewrite them (spec
051). Both implementations read these bytes: Python opens the files, Go embeds
them, and a test compares what each makes of them row by row while both exist.

The directory sits beside the Python package rather than inside it, which is
where it was. It moved for two reasons, and the second forced it: the data now
belongs to the repository rather than to one implementation, and Go can embed
only what is under its own package directory.
*/
package data

import "embed"

/*
FS is the tables, read-only.

Embedded rather than read from disk, because the point of the port is a binary
that needs nothing beside it: a trainer being used to recover a character should
not fail because a data directory was left behind.
*/
//go:embed *.json
var FS embed.FS

// The file names, so a caller names a table rather than spelling a path.
const (
	Items       = "items.json"
	Tooltips    = "tooltips.json"
	NPCs        = "npcs.json"
	Prefixes    = "prefixes.json"
	PrefixStats = "prefix_stats.json"
	Recipes     = "recipes.json"
	Tiles       = "tiles.json"
)
