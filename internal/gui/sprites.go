package gui

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"fyne.io/fyne/v2"
)

/*
The item icon cache.

sprites.py decodes the game's own Item_<id>.xnb into PNGs under ~/.cache, and
this reads those files. No Python at runtime and no decoding here: the window
loads PNGs the extractor already wrote, and shows an abbreviated name for
anything it has not.

The cache is under ~/.cache rather than ~/.config deliberately -- extraction is
unprivileged, while the config directory can be root-owned from a sudo memory
command, which would make it unwritable.
*/
type sprites struct {
	dir string

	mu     sync.Mutex
	loaded map[int]fyne.Resource // nil value means "looked, not there"
}

// spriteVersion is the game build the cache is keyed by. It matches
// version.KNOWN_VERSION on the Python side; a mismatch means icons for a build
// this window is not looking at, so the miss is the right answer.
const spriteVersion = "1.4.5.8"

// newSprites points at the cache without reading it. Nothing is loaded until a
// cell asks for an icon.
func newSprites() *sprites {
	home, err := os.UserHomeDir()
	if err != nil {
		return &sprites{loaded: map[int]fyne.Resource{}}
	}
	return &sprites{
		dir:    filepath.Join(home, ".cache", "terrariabonker", "sprites", spriteVersion),
		loaded: map[int]fyne.Resource{},
	}
}

/*
icon returns an item's icon, or nil when the cache has not got one.

Read once per item and remembered, including the misses: the grid asks for the
same icons every second, and a miss that is not remembered is a stat() per empty
answer per redraw.
*/
func (s *sprites) icon(itemType int) fyne.Resource {
	if s == nil || s.dir == "" || itemType == 0 {
		return nil
	}
	s.mu.Lock()
	if r, seen := s.loaded[itemType]; seen {
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()

	var res fyne.Resource
	path := filepath.Join(s.dir, "Item_"+strconv.Itoa(itemType)+".png")
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // a path this package built from an int
		res = fyne.NewStaticResource(filepath.Base(path), data)
	}

	s.mu.Lock()
	s.loaded[itemType] = res
	s.mu.Unlock()
	return res
}

// ready reports whether the cache has been built at all. The extractor writes a
// marker when it finishes, so a half-written cache does not read as a whole one.
func (s *sprites) ready() bool {
	if s == nil || s.dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(s.dir, ".done"))
	return err == nil
}
