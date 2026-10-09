package service

import (
	"fmt"

	"github.com/ushineko/terrariabonker/internal/inventory"
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The build gate: which build is running, and whether this program's numbers were
derived against it.

It is a gate on *writing*, not on reading. A read against a build whose offsets
have moved finds no player, because the locator validates every match; a write
against one puts numbers into the wrong fields of a live save.
*/

// Build is what is known about the running game's build.
type Build struct {
	Version string        `json:"version"`
	BuildID string        `json:"buildid"`
	Level   version.Level `json:"compat_level"`
	Message string        `json:"compat_msg"`
}

/*
BuildInfo is the running build, worked out once it reads as a real one.

Cached only then. The version is scanned out of the process, and during startup
the runtime's own version can outnumber the game's before the game has loaded its
string -- caching that would pin the build at "incompatible" for the life of this
service and wedge every mutating operation behind the gate, restore included. So
an unknown or incompatible reading is re-detected on the next call instead.
*/
func (s *Service) BuildInfo() Build {
	if s.build != nil {
		return *s.build
	}
	found := version.Detect(s.Mem, s.PID)
	buildid := version.ReadBuildID(s.Mem.ExePath())
	level, msg := version.Compatibility(found, buildid)
	info := Build{Version: found, BuildID: buildid, Level: level, Message: msg}
	if level == version.Exact || level == version.Hotfix {
		s.build = &info
	}
	return info
}

// Compatibility is how far the running build is from the known-good one, and
// why.
func (s *Service) Compatibility() (version.Level, string) {
	info := s.BuildInfo()
	return info.Level, info.Message
}

/*
RequireCompatible refuses to go on when the offsets almost certainly do not fit
the running build.

Only an outright incompatible build is refused, and only when not forced. An
unknown one is allowed through: it means the version could not be read, which
happens during startup and is not evidence of anything.

An entry that may not write -- no entry covers the runtime, or its write paths
do not exist yet -- is refused whether forced or not. Forcing is for a build whose offsets *might* still fit; numbers derived
under one runtime are measured not to fit another, so there is nothing to force.
*/
func (s *Service) RequireCompatible(force bool) error {
	if entry, support, runtime := s.Support(); !entry.Writes {
		return &Error{Message: noWrites(support, runtime)}
	}
	return s.requireBuild(force)
}

/*
RequireWritable is RequireCompatible for one kind of write: it passes under an
entry that allows that write even when the entry may not make every write. The
CLR entry writes player stats this way while its other write paths do not exist.
*/
func (s *Service) RequireWritable(f layout.Feature, force bool) error {
	if entry, support, runtime := s.Support(); !entry.CanWrite(f) {
		return &Error{Message: noWrites(support, runtime)}
	}
	return s.requireBuild(force)
}

// requireBuild refuses an outright incompatible build unless forced.
func (s *Service) requireBuild(force bool) error {
	level, msg := s.Compatibility()
	if level == version.Incompatible && !force {
		return &Error{Message: fmt.Sprintf(
			"%s. Re-derive offsets (docs/discovery.md) or force to override.", msg)}
	}
	return nil
}

/*
Support is the version table's answer for the running game: the entry its build
and runtime select, how well they fit, and the runtime as detected.

The runtime is kept once read: it cannot change while the process lives, and
reading it lists the process's modules.
*/
func (s *Service) Support() (layout.Entry, layout.Support, string) {
	if s.runtime == "" {
		s.runtime = detectRuntime(s.PID)
	}
	entry, support := layout.Select(s.BuildKey(), s.runtime)
	return entry, support, s.runtime
}

/*
noWrites is what a refusal says when the selected entry may not write: either no
entry covers the runtime, or one does and its write paths do not exist yet.
*/
func noWrites(support layout.Support, runtime string) string {
	if support == layout.Unsupported {
		return fmt.Sprintf("Terraria is running on %s, and this version of terrariabonker has no "+
			"memory layout for that runtime, so nothing was changed. The same game under a "+
			"different .NET runtime lays its memory out differently (spec 052)", runtime)
	}
	return fmt.Sprintf("Terraria is running on %s, which this version of terrariabonker can only "+
		"read so far, so nothing was changed (spec 052)", runtime)
}

// CanRead reports whether the selected entry's numbers can read a feature. A
// reader of that feature asks first: under an entry that cannot read it, the
// numbers it would use are another runtime's.
func (s *Service) CanRead(f layout.Feature) bool {
	entry, _, _ := s.Support()
	return entry.CanRead(f)
}

// locator is the player locator for the selected entry.
func (s *Service) locator() locate.Locator {
	entry, _, _ := s.Support()
	return locate.With(entry)
}

// WithRuntime sets the runtime instead of detecting it, for a caller that
// already knows it: a front end that read it once, or a test whose pid is not a
// game. Detection is skipped from then on.
func (s *Service) WithRuntime(runtime string) *Service {
	s.runtime = runtime
	return s
}

// inventoryAt is the inventory of the player copy whose statLife is at life, read
// with the selected entry's numbers.
func (s *Service) inventoryAt(life uint32) *inventory.Inventory {
	entry, _, _ := s.Support()
	return inventory.NewFor(entry, s.Mem, life)
}

// CanWrite reports whether the selected entry allows a kind of write.
func (s *Service) CanWrite(f layout.Feature) bool {
	entry, _, _ := s.Support()
	return entry.CanWrite(f)
}

/*
Equip points a patcher at the selected entry -- its cheat sites, anchors and
injection set -- and at the live character's copies for a cheat's value, the
same targets every other write takes. A game attached only to read has no
patcher, and there is nothing to equip.
*/
func (s *Service) Equip(p *patch.Patcher) {
	if p == nil {
		return
	}
	entry, _, _ := s.Support()
	p.UseEntry(entry)
	p.Targets = func() ([]uint32, error) {
		blocks, err := s.writeTargets()
		if err != nil {
			return nil, err
		}
		out := make([]uint32, len(blocks))
		for i, b := range blocks {
			out[i] = b.LifeAddr
		}
		return out, nil
	}
}
