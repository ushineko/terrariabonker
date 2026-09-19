package sprites_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
Cropping a sheet down to one picture.

Every rule here is a judgement about a picture, and a judgement that is slightly
wrong produces an icon that is slightly wrong -- half a slime, a row of little
pictures, a staff cut in two -- which nobody reports as a bug because it looks
like the game's own art.

So each case names the size and the pixels it produces. Those were agreed with
the implementation this was ported from, while both existed, and are frozen
here: a change to any of these rules is a change to what an icon looks like, and
is meant to be a decision.
*/

// strip is a vertical animation: blocks of opaque rows in equal slots, the
// remainder transparent.
func strip(width, frameH, frames, contentH int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, frameH*frames))
	for k := range frames {
		for y := k * frameH; y < k*frameH+contentH; y++ {
			for x := range width {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	return img
}

// grid is a sheet of equal cells with transparent gutters between them.
func grid(cellW, cellH, cols, rows, gutter int) *image.NRGBA {
	w := cols*cellW + (cols-1)*gutter
	h := rows*cellH + (rows-1)*gutter
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for r := range rows {
		for c := range cols {
			x0, y0 := c*(cellW+gutter), r*(cellH+gutter)
			for y := y0; y < y0+cellH; y++ {
				for x := x0; x < x0+cellW; x++ {
					shade := uint8(16*r + 4*c + 1) //nolint:gosec // a small index
					img.SetNRGBA(x, y, color.NRGBA{R: shade, G: shade, B: shade, A: 255})
				}
			}
		}
	}
	return img
}

// bands is an image with equal horizontal bands of content, evenly spaced.
func bands(w, h, count, thick int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	pitch := h / count
	for k := range count {
		for y := k * pitch; y < k*pitch+thick && y < h; y++ {
			for x := range w {
				img.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
			}
		}
	}
	return img
}

// blocksAt is an image with content exactly where it is asked for.
func blocksAt(w, h int, runs [][2]int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for _, run := range runs {
		for y := run[0]; y < run[1] && y < h; y++ {
			for x := range w {
				img.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	return img
}

// blob is one lump of content in an otherwise empty image, for the cases that
// must be left alone.
func blob(w, h, x0, y0, x1, y1 int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	return img
}

// shape is one image's size and the hash of its pixels.
type shape struct {
	Size string `json:"size"`
	Sum  string `json:"sum"`
}

func describe(t *testing.T, img image.Image) shape {
	t.Helper()
	b := img.Bounds()
	pix := make([]byte, 0, b.Dx()*b.Dy()*4)
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			pix = append(pix, c.R, c.G, c.B, c.A)
		}
	}
	sum := sha256.Sum256(pix)
	return shape{
		Size: itoa(b.Dx()) + "x" + itoa(b.Dy()),
		Sum:  hex.EncodeToString(sum[:8]),
	}
}

func itoa(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// De-animating a strip takes its first frame, and leaves everything else alone.
func TestDeanimate(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
		want shape
	}{
		{"a four-frame strip", strip(10, 12, 4, 10), shape{"10x12", "14dbc99a7fd4c520"}},
		{"a two-frame strip", strip(16, 20, 2, 18), shape{"16x20", "1e41585be82bfc1f"}},
		{"frames that fill their slots", strip(8, 8, 5, 8), shape{"8x40", "f232749538acee34"}},
		{"one tall block, like a staff", blob(10, 30, 0, 5, 10, 25), shape{"10x30", "9995cb9c350b9078"}},
		{"wider than it is tall", blob(32, 20, 0, 0, 32, 20), shape{"32x20", "8f7448c93b145223"}},
		/*
			Two even bands in a wide image, which is not a strip.

			Without the shape test this is exactly what a strip looks like from
			the inside, and it would be cropped in half -- a sword with a
			separate hilt, cut at the grip.
		*/
		{"two bands in a wide image", bands(32, 20, 2, 8), shape{"32x20", "4c054037b0721055"}},
		// And a strip whose height does not divide by the blocks in it.
		{"blocks that do not divide the height", bands(10, 25, 2, 8), shape{"10x25", "8c49285e89d7f725"}},
		{"a strip whose blocks straddle their slots", strip(10, 12, 3, 14), shape{"10x36", "333e0d6cb69b42d3"}},
		/*
			Three separate blocks that do not line up with their frames.

			The count divides the height and the blocks are distinct, so
			everything but their positions says "strip" -- and cropping to the
			first twelve rows would cut the second block in half.
		*/
		{"blocks that sit across the frame lines", blocksAt(10, 36,
			[][2]int{{0, 6}, {10, 16}, {24, 30}}), shape{"10x36", "074185326ae1d359"}},
		{"nothing in it at all", image.NewNRGBA(image.Rect(0, 0, 10, 40)), shape{"10x40", "e61f41d57db208c5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, describe(t, sprites.Deanimate(tc.img)),
				"a different crop; if that was deliberate, update the shape")
		})
	}
}

/*
And a strip really is cropped, rather than the two agreeing to leave everything
alone.

Two implementations that both returned the image unchanged would pass every
comparison above.
*/
func TestDeanimateCropsAStrip(t *testing.T) {
	got := sprites.Deanimate(strip(10, 12, 4, 10))
	require.Equal(t, 12, got.Bounds().Dy(), "the strip was not cropped to one frame")
	require.Equal(t, 10, got.Bounds().Dx(), "the width changed")

	staff := blob(10, 30, 0, 5, 10, 25)
	require.Equal(t, 30, sprites.Deanimate(staff).Bounds().Dy(),
		"a single-frame item was cropped")
}

// Cropping an NPC sheet with the game's own frame count matches.
func TestFirstFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		img    image.Image
		frames int32
		want   shape
	}{
		{"a two-frame vertical strip", strip(32, 26, 2, 24), 2, shape{"32x26", "519b937a115d0945"}},
		{"one frame, left alone", blob(573, 804, 20, 20, 500, 700), 1, shape{"573x804", "642d665e853f0cec"}},
		{"sixteen frames in two columns", grid(30, 40, 2, 8, 2), 16, shape{"30x41", "4fef6547410e9390"}},
		{"a three by three grid", grid(24, 24, 3, 3, 4), 9, shape{"24x26", "b42429be6490b201"}},
		{"five columns", grid(20, 30, 5, 2, 2), 10, shape{"20x31", "a1b7eab760314d55"}},
		{"a count that does not divide the height", strip(16, 20, 3, 18), 7, shape{"16x8", "9f56cda75fefeab9"}},
		{"frames too short to be frames", strip(16, 2, 8, 2), 8, shape{"16x16", "5f4ecdb7b71c3e40"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, describe(t, sprites.FirstFrame(tc.img, tc.frames)),
				"a different crop; if that was deliberate, update the shape")
		})
	}
}

/*
A grid is cropped to its top-left cell, and one sprite with a detached piece is
not.

That second case is what the evenness rule exists for: the blocks of a grid are
the same size and the pieces of one sprite are not.
*/
func TestFirstGridCell(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
		want shape
	}{
		{"a three by three grid", grid(24, 24, 3, 3, 4), shape{"24x24", "21984266105bb7ae"}},
		{"two columns", grid(30, 40, 2, 1, 6), shape{"30x40", "7f83105557a0c568"}},
		{"one sprite with a detached piece", detached(), shape{"60x30", "bf96d6109cf92884"}},
		{"one solid block", blob(40, 40, 0, 0, 40, 40), shape{"40x40", "8e934f054326c8c6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, describe(t, sprites.FirstGridCell(tc.img)),
				"a different crop; if that was deliberate, update the shape")
		})
	}
}

// detached is one sprite whose pieces are very different sizes, which is not a
// grid.
func detached() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 60, 30))
	for y := range 30 {
		for x := range 40 {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	for y := 10; y < 16; y++ {
		for x := 50; x < 54; x++ {
			img.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	return img
}

// A chest is assembled out of the four tiles of its style.
func TestCompositeChest(t *testing.T) {
	sheet := grid(16, 16, 8, 6, 2)
	for _, c := range []struct {
		style int
		want  shape
	}{
		{0, shape{"32x32", "8ba3de936ae89581"}},
		{1, shape{"32x32", "7e472f59772110a0"}},
		// Past the end of a row, which is where the wrap is.
		{3, shape{"32x32", "6b8ab95bc2f2af72"}},
		{7, shape{"32x32", "50cad851b4a1befc"}},
	} {
		t.Run("style "+itoa(c.style), func(t *testing.T) {
			require.Equal(t, c.want, describe(t, sprites.CompositeChest(sheet, c.style)),
				"a different chest; if that was deliberate, update the shape")
		})
	}
}

/*
The tint is the game's own: the sheet is attenuated and the colour added on top.

Multiplying instead comes out far too dark to read at icon size, which is a
thing that looks like a rendering bug rather than a wrong formula.
*/
func TestTinted(t *testing.T) {
	sheet := grid(12, 12, 2, 2, 2)
	for _, c := range []struct {
		tint [4]byte
		want shape
	}{
		{[4]byte{0, 80, 255, 100}, shape{"26x26", "bf08c46eef912a01"}},
		{[4]byte{102, 204, 106, 255}, shape{"26x26", "ec4cf7bea2500bb4"}},
		// No tint at all still attenuates, which is what makes an untinted
		// sheet visibly different from one nobody painted.
		{[4]byte{0, 0, 0, 0}, shape{"26x26", "1f7b1ce59b912837"}},
		{[4]byte{255, 255, 255, 255}, shape{"26x26", "ac240fe59cec8b38"}},
	} {
		name := itoa(int(c.tint[0])) + "," + itoa(int(c.tint[1])) + "," + itoa(int(c.tint[2]))
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, describe(t, sprites.Tinted(sheet, c.tint)),
				"a different painting; if that was deliberate, update the shape")
		})
	}
}

// quote is a path as a JSON string, for writing one into a file this test
// builds.
func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

/*
realHome is the home directory before a test moved it.

The learned content path lives there, and every test here points HOME at a
scratch directory -- so the one thing that has to be read from the real one is
read through this.
*/
var realHome = os.Getenv("HOME")
