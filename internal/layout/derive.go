package layout

import (
	"maps"
	"slices"
)

/*
Derive is an entry made from another: the base's numbers, then change's edits.

This is how a version that differs from a supported one in a few places is
added -- a game update that moved one method, a runtime update that recompiled
it differently -- so the new entry states only what differs, and everything it
carries over unchanged is carried over on purpose, by whoever adds it.

Every map and list is copied before change sees the entry. An Entry is a value,
but its maps and slices are shared with the value it was copied from, so a plain
copy whose Cheats were edited would edit the base's too.
*/
func Derive(base Entry, change func(*Entry)) Entry {
	e := base
	e.Builds = slices.Clone(base.Builds)
	e.Versions = slices.Clone(base.Versions)
	e.Confirmed = slices.Clone(base.Confirmed)
	e.Reads = slices.Clone(base.Reads)
	e.WriteFeatures = slices.Clone(base.WriteFeatures)
	e.PlayerValues = maps.Clone(base.PlayerValues)
	e.Cheats = make(map[string]CheatSite, len(base.Cheats))
	for name, site := range base.Cheats {
		site.Orig, site.Patched = slices.Clone(site.Orig), slices.Clone(site.Patched)
		e.Cheats[name] = site
	}
	e.Anchors = make(map[string]AnchorDef, len(base.Anchors))
	for key, def := range base.Anchors {
		def.Verified = slices.Clone(def.Verified)
		e.Anchors[key] = def
	}
	change(&e)
	return e
}
