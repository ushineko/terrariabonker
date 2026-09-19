package buffs_test

import (
	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

// The two pointers, from the one table that declares them.
var (
	layoutBuffTypePtr = layout.Offsets["BUFF_TYPE_PTR_OFF"]
	layoutBuffTimePtr = layout.Offsets["BUFF_TIME_PTR_OFF"]
)

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// watcher records where writes went, for the test about which of two goes first.
type watcher struct {
	*memtest.FakeMem
	writes []uint32
}

func (w *watcher) Write(addr uint32, data []byte) bool {
	w.writes = append(w.writes, addr)
	return w.FakeMem.Write(addr, data)
}
