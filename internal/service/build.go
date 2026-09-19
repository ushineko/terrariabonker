package service

import (
	"fmt"

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
*/
func (s *Service) RequireCompatible(force bool) error {
	level, msg := s.Compatibility()
	if level == version.Incompatible && !force {
		return &Error{Message: fmt.Sprintf(
			"%s. Re-derive offsets (docs/discovery.md) or force to override.", msg)}
	}
	return nil
}
