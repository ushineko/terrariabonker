package tiles_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
Under .NET Framework the tile map is a Tile[,] of references: dims inline in the
array object (lenX at +0x08, lenY at +0x0C), data at +0x18, each element a Tile
pointer, with type at +0x04 and sTileHeader (active bit 0x20) at +0x08.

A 3x2 world is planted at the CLR offsets, reached through Main.tile's reference
static slot, and TypeAt/ActiveAt read what was planted. The index is
column-major (stride = lenY), as on mono.
*/
func TestCLRTileReads(t *testing.T) {
	const clrBase = 0x10000000
	mem := memtest.New(clrBase, 0x4000)

	const slot = clrBase + 0x40 // Main.tile reference-static slot
	const arr = clrBase + 0x100 // the Tile[,] array object
	const lenX, lenY = int32(3), int32(2)
	mem.PokeI32(slot, arr)
	mem.PokeI32(arr+0x00, 0x0823b660) // MethodTable (unused by the reader)
	mem.PokeI32(arr+0x04, lenX*lenY)  // total count
	mem.PokeI32(arr+0x08, lenX)       // dim0
	mem.PokeI32(arr+0x0C, lenY)       // dim1 (stride)
	mem.PokeI32(arr+0x10, 0)          // lowerBound0
	mem.PokeI32(arr+0x14, 0)          // lowerBound1
	data := uint32(arr + 0x18)

	// one tile object per (x,y), id = x*10+y, active everywhere but (2,1)
	obj := uint32(clrBase + 0x800)
	for x := int32(0); x < lenX; x++ {
		for y := int32(0); y < lenY; y++ {
			p := obj + uint32(x*lenY+y)*0x18                 //nolint:gosec // a small index
			mem.PokeI32(data+4*uint32(x*lenY+y), int32(p))   //nolint:gosec // a planted pointer
			mem.PokeBytes(p+0x04, []byte{byte(x*10 + y), 0}) // type (ushort)
			hdr := byte(0x20)
			if x == 2 && y == 1 {
				hdr = 0
			}
			mem.PokeBytes(p+0x08, []byte{hdr, 0}) // sTileHeader
		}
	}

	tm, err := tiles.New(mem, clrEntry(), slot)
	require.NoError(t, err)
	require.Equal(t, [2]int32{lenX, lenY}, [2]int32{tm.MaxX, tm.MaxY})

	for x := int32(0); x < lenX; x++ {
		for y := int32(0); y < lenY; y++ {
			id, ok := tm.TypeAt(x, y)
			require.True(t, ok, "no tile at %d,%d", x, y)
			require.EqualValues(t, x*10+y, id, "type at %d,%d", x, y)
		}
	}
	active, ok := tm.ActiveAt(2, 1)
	require.True(t, ok)
	require.False(t, active, "(2,1) was planted inactive")
	active, _ = tm.ActiveAt(0, 0)
	require.True(t, active)
}

func clrEntry() layout.Entry {
	e, _ := layout.Select(layout.Build1458s24893155, "netfx-4.8.9345.0")
	return e
}
