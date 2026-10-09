package patch

import (
	"fmt"
	"time"
)

/*
How an arena's memory comes to exist, by the name an injection set gives.

Finding an arena again, choosing its address and stamping it are the same
everywhere (Patcher.ArenaWithin). Getting the memory mapped is not: under Proton
this program cannot map anything in the game, so the mono set has the game call
VirtualAlloc for it through a springboard, which needs frames; natively on
Windows the program allocates in the game directly. Each is a module here, and a
set names one.
*/
type arenaMaker func(p *Patcher, base uint32, wait time.Duration) error

var arenaMakers = map[string]arenaMaker{
	"springboard": springboardArena,
	"allocate":    allocateArena,
}

/*
springboardArena has the game allocate at base, through a per-frame hook. The
game must be running frames: Terraria pauses in single-player whenever its window
loses focus.
*/
func springboardArena(p *Patcher, base uint32, wait time.Duration) error {
	if err := p.bootstrapArena(base, wait); err != nil {
		return err
	}
	if _, ok := Mapped(p.Mem, base); !ok {
		return fmt.Errorf("VirtualAlloc did not run. The springboard sits on a " +
			"per-frame path, so this means the game is not advancing frames -- Terraria " +
			"pauses in single-player whenever its window loses focus. Focus the game " +
			"and try again")
	}
	return nil
}

// Allocator is memory this program can map pages in: a native Windows process.
type Allocator interface {
	AllocateAt(addr uint32, size int) error
}

// allocateArena maps the arena at base directly. It needs no frames.
func allocateArena(p *Patcher, base uint32, _ time.Duration) error {
	a, ok := p.Mem.(Allocator)
	if !ok {
		return fmt.Errorf("this game's memory cannot be allocated in from here")
	}
	return a.AllocateAt(base, ArenaSize)
}
