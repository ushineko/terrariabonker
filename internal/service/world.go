package service

import (
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/tiles"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The world: where Main's statics are, what the tiles look like, and which world is
loaded.
*/

/*
StaticBase is Main's static block, scanned once and remembered.

Finding it is a full memory scan costing over a second. The statics do not move
while the process lives, so every caller shares one answer -- the world identity
is read on every status poll, and paying for a scan each time would be the whole
budget.
*/
func (s *Service) StaticBase() (uint32, bool) {
	if !s.haveBase {
		s.mainBase, s.haveBase = locate.MainStaticBase(s.Mem)
	}
	return s.mainBase, s.haveBase
}

/*
TileMap is a read-only view of the world's tiles.

The expensive half is finding the statics; building the view from them is six
reads. So the base is kept and only the cheap part is redone, which matters
because the vein watcher asks for this on every trigger -- paying the scan there
made the extractor look like it had a delay when the mining itself takes
milliseconds.

A world change moves the tile buffer, not the statics, so the view is still
rebuilt each call.
*/
func (s *Service) TileMap() (*tiles.TileMap, error) {
	base, ok := s.StaticBase()
	if !ok {
		return nil, &Error{Message: "could not locate Main's statics -- is the game in a world?"}
	}
	tm, err := tiles.New(s.Mem, base)
	if err != nil {
		// Stale, or no world loaded: scan again next time rather than keeping a
		// base that no longer describes anything.
		s.haveBase = false
		return nil, &Error{Message: err.Error()}
	}
	return tm, nil
}

// World is which world is loaded.
type World struct {
	Name   string `json:"name"`
	Width  int32  `json:"width"`
	Height int32  `json:"height"`
}

/*
WorldID is the loaded world, and whether one could be identified.

The name carries the identity and the dimensions corroborate it. **The dimensions
alone are not enough, and neither is the tile buffer's address**: this used to be
keyed on the buffer and the two dimensions, and that is byte-identical across a
switch between two worlds of the same size, which is exactly what a measurement
showed.

It fails safe. A rotted offset gives a null or unreadable pointer, the name comes
back empty, and callers fall back to their old behaviour rather than acting on a
wrong answer.
*/
func (s *Service) WorldID() (World, bool) {
	base, ok := s.StaticBase()
	if !ok {
		return World{}, false
	}
	ptr, ok := s.Mem.ReadU32(base + layout.MainWorldNameOff)
	if !ok {
		return World{}, false
	}
	name, ok := locate.ReadMonoString(s.Mem, ptr)
	if !ok || name == "" {
		return World{}, false
	}
	tm, err := s.TileMap()
	if err != nil {
		return World{}, false
	}
	return World{Name: name, Width: tm.MaxX, Height: tm.MaxY}, true
}

// BuildKey is the identity of the running build.
func (s *Service) BuildKey() string {
	info := s.BuildInfo()
	return version.BuildKey(info.Version, info.BuildID)
}
