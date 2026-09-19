package service

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/proc"
	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
The catalog: every item and every NPC, with the stats the game itself holds.

Names come from the executable and are already bundled. Stats do not exist in
any readable form there -- they are assigned at runtime -- so they are read out
of the running game's own template objects, which costs a scan of the writable
regions. That is about two seconds, once per build, so the result goes in a
cache keyed by the build it was read from.

The cache lives under ~/.cache rather than the config directory because the
scan runs privileged and the config directory can end up root-owned; the file
and the directory around it are handed back to the invoking user.
*/

// CatalogItem is one item as the catalog lists it.
type CatalogItem struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Tooltip string `json:"tooltip"`
	Stats   any    `json:"stats"`
	Wiki    string `json:"wiki"`
}

// CatalogNPC is one NPC. It carries `npc` so a merged list can still be told
// apart, which is what the search box does with it.
type CatalogNPC struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	NPC   bool   `json:"npc"`
	Kind  string `json:"kind"`
	Stats any    `json:"stats"`
	Wiki  string `json:"wiki"`
}

// Catalog is both lists and the build they were read from.
type Catalog struct {
	Items []CatalogItem `json:"items"`
	NPCs  []CatalogNPC  `json:"npcs"`
	Build string        `json:"build"`
}

/*
giveBack hands a path this program wrote as root back to the invoking user.

Named rather than called directly so a test can watch for it: none of it can
happen unless the test is root, and a helper nothing invokes is the failure
worth catching here.
*/
var giveBack = proc.GiveBackToUser

/*
noStats is what an entry carries when the game had no template for it.

An empty object rather than a zeroed one: a zero is a real value for every field
here, and "defense 0, head slot 0" reads as a hat that protects nothing rather
than as a thing nobody has stats for.
*/
var noStats = struct{}{}

/*
Compendium is the whole catalog, from cache when there is one for this build.

`refresh` rescans and rewrites, which is what a cache written by an older
version of this program needs.
*/
func (s *Service) Compendium(refresh bool) (Catalog, error) {
	itemNames, err := game.ItemNames()
	if err != nil {
		return Catalog{}, &Error{Message: err.Error()}
	}
	npcNames, err := game.NPCs()
	if err != nil {
		return Catalog{}, &Error{Message: err.Error()}
	}

	stats := s.itemTemplateCache(refresh)
	items := make([]CatalogItem, 0, itemNames.Len())
	for _, id := range sortedIDs(itemNames.All()) {
		entry := CatalogItem{
			ID: id, Name: itemNames.Name(id), Kind: "Unknown",
			Tooltip: itemNames.Tooltip(id), Stats: noStats,
			Wiki: content.WikiURL(itemNames.Name(id)),
		}
		if st, known := stats[int32(id)]; known { //nolint:gosec // an item id
			entry.Kind, entry.Stats = content.ItemKind(st), st
		}
		items = append(items, entry)
	}

	npcStats := s.npcTemplateCache(refresh)
	s.publishNPCDrawData(npcStats)
	npcs := make([]CatalogNPC, 0, npcNames.Count())
	for _, id := range sortedIDs(npcNames.All()) {
		entry := CatalogNPC{
			ID: id, Name: npcNames.Name(id), NPC: true, Kind: "NPC",
			Stats: noStats, Wiki: content.WikiURL(npcNames.Name(id)),
		}
		if st, known := npcStats[int32(id)]; known { //nolint:gosec // an NPC netID
			entry.Kind, entry.Stats = content.NPCKind(st), st
		}
		npcs = append(npcs, entry)
	}
	return Catalog{Items: items, NPCs: npcs, Build: s.BuildKey()}, nil
}

// sortedIDs is a name table's ids in order, so the catalog does not shuffle
// between calls.
func sortedIDs(names map[int]string) []int {
	out := make([]int, 0, len(names))
	for id := range names {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

/*
itemTemplateCache is the item stats, cached per build.

`wants` names three fields a cache written before they were read does not have,
and without them an edit cannot be told from an item's own defaults. Such a
cache is rescanned rather than guessed at.
*/
func (s *Service) itemTemplateCache(refresh bool) map[int32]content.ItemStats {
	return templateCache(s, "templates", refresh,
		[]string{"use_anim", "auto_reuse", "tile_boost"},
		func() map[int32]content.ItemStats {
			vt, ok := s.ItemVTable()
			if !ok {
				return map[int32]content.ItemStats{}
			}
			return content.FindItemTemplates(s.Mem, vt, s.ownItemAddrs())
		})
}

// npcTemplateCache is the same for NPCs, keyed on netID as the game's own
// sample collection is.
func (s *Service) npcTemplateCache(refresh bool) map[int32]content.NPCStats {
	return templateCache(s, "npcs", refresh, nil,
		func() map[int32]content.NPCStats {
			base, ok := s.StaticBase()
			if !ok {
				return map[int32]content.NPCStats{}
			}
			vt, ok := content.FindNPCVTable(s.Mem, base)
			if !ok {
				return map[int32]content.NPCStats{}
			}
			return content.FindNPCTemplates(s.Mem, vt, s.liveNPCAddrs())
		})
}

/*
templateCache is read-or-scan around one catalog.

A free function rather than a method because it is generic over what it holds,
and the two catalogs do not share a stats type.

Not caching by address, which would be the cheaper thing: the managed heap is
collected, so an address that held a template stops being one. Numbers survive
that, which is why this caches the parsed stats rather than where they were.
*/
func templateCache[T any](s *Service, kind string, refresh bool, wants []string,
	scan func() map[int32]T) map[int32]T {
	path := cachePath(kind, s.BuildKey())
	if !refresh {
		if got, ok := readCache[T](path, wants); ok {
			return got
		}
	}
	found := scan()
	writeCache(path, found)
	return found
}

// cachePath is where one catalog's cache goes. The build key carries a "+",
// which is not a character to put in a filename.
func cachePath(kind, build string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "terrariabonker",
		kind+"-"+strings.ReplaceAll(build, "+", "-")+".json")
}

/*
readCache is a cache that is both readable and complete enough to use.

`wants` is checked against the first entry in the file rather than against all
of them: they are written in one go by one version of this program, so either
all of them have a field or none do -- and the first is what the Python samples,
which is why the file's own order is what is read rather than a map's.
*/
func readCache[T any](path string, wants []string) (map[int32]T, bool) {
	if path == "" {
		return nil, false
	}
	blob, err := os.ReadFile(path) //nolint:gosec // a path this package built
	if err != nil {
		return nil, false
	}
	if len(wants) > 0 {
		sample, found := firstEntry(blob)
		for _, want := range wants {
			if found && sample[want] == nil {
				return nil, false
			}
		}
	}
	var raw map[string]T
	if err := json.Unmarshal(blob, &raw); err != nil {
		return nil, false
	}
	out := make(map[int32]T, len(raw))
	for key, v := range raw {
		id, err := strconv.Atoi(key)
		if err != nil {
			return nil, false
		}
		out[int32(id)] = v //nolint:gosec // an id this program wrote
	}
	return out, true
}

/*
firstEntry is the file's first value, as the fields it happens to carry.

Streamed rather than unmarshalled into a map, because "first" has to mean first
in the file: a map would hand back a different entry on every run and a cache
would be rescanned or not at random.
*/
func firstEntry(blob []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(blob))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	// The first key, or the closing brace of a catalog with nothing in it.
	if _, err := dec.Token(); err != nil || !dec.More() {
		return nil, false
	}
	var entry map[string]json.RawMessage
	if err := dec.Decode(&entry); err != nil {
		return nil, false
	}
	return entry, true
}

/*
writeCache saves a catalog and hands it back to the user who asked for it.

Written to a temporary file and renamed, so a reader never sees half of one. A
cache that cannot be written is not a failure: the scan already produced the
answer, and the only cost is doing it again next time.
*/
func writeCache[T any](path string, found map[int32]T) {
	if path == "" {
		return
	}
	raw := make(map[string]T, len(found))
	for id, v := range found {
		raw[strconv.Itoa(int(id))] = v
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil { //nolint:gosec // read unprivileged
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		return
	}
	/*
		This runs privileged but writes into the user's own cache directory --
		`sudo -E` keeps HOME -- so hand the result back or they cannot clear
		their own cache.
	*/
	giveBack(dir)
	giveBack(path)
}

/*
publishNPCDrawData hands the icon extractor what only this side can read.

Main.npcFrameCount and NPC.color are in the game's memory; extraction runs
without sudo. Written through the same give-back path as the caches, for the
same reason.
*/
func (s *Service) publishNPCDrawData(npcStats map[int32]content.NPCStats) {
	base, ok := s.StaticBase()
	if !ok {
		return
	}
	counts := content.FrameCounts(s.Mem, base)
	if len(counts) == 0 {
		return
	}
	frames := make(map[int32]int32, len(counts))
	for npcType, n := range counts {
		frames[int32(npcType)] = n //nolint:gosec // an NPC type
	}
	tints := map[int32]sprites.NPCTint{}
	for netID, st := range npcStats {
		if st.Color != [4]byte{} {
			tints[netID] = sprites.NPCTint{Type: st.Type, Color: st.Color}
		}
	}
	if err := sprites.SaveNPCDrawData(frames, tints); err != nil {
		return
	}
	giveBack(sprites.NPCFramesFile())
}
