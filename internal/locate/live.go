package locate

import "github.com/ushineko/terrariabonker/internal/layout"

/*
LiveFinder is one runtime's way of telling which player copy is the live one:
Main.player[Main.myPlayer], reached through something that, once found, leads
there with a few reads.

Each way is its own module -- mono's in localplayer.go, the CLR's in
clrstatics.go -- and an entry names the one it uses (layout.LocalPlayerBy). A
runtime that finds the live player another way is another module and another
name here, not a branch in the code that asks.
*/
type LiveFinder interface {
	// Find is the anchor, found from scratch: the expensive half. copies are the
	// player copies a scan found, for a way that starts from them.
	Find(mem ExecMem, copies []Block) (uint32, bool)
	// At is the live player through an anchor already found: the cheap half,
	// and false when the anchor has stopped leading there.
	At(mem Mem, anchor uint32) (Block, bool)
}

// liveFinders is every way, by the name an entry gives.
var liveFinders = map[layout.LocalPlayerBy]func(Locator) LiveFinder{
	layout.ByAnchor:  func(Locator) LiveFinder { return getLocalPlayer{} },
	layout.ByStatics: func(l Locator) LiveFinder { return mainStatics{l} },
}

/*
Live is the locator's entry's way of finding the live player. An entry that
names none -- the zero entry an unsupported runtime selects -- finds nothing,
which is the same answer as a way that did not work.
*/
func (l Locator) Live() LiveFinder {
	if newFinder, ok := liveFinders[l.localPlayer]; ok {
		return newFinder(l)
	}
	return noLive{}
}

// noLive finds nothing.
type noLive struct{}

func (noLive) Find(ExecMem, []Block) (uint32, bool) { return 0, false }
func (noLive) At(Mem, uint32) (Block, bool)         { return Block{}, false }
