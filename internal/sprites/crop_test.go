package sprites_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
Cropping a sheet down to one picture.

Every rule here is a judgement about a picture, and a judgement that is slightly
wrong produces an icon that is slightly wrong -- half a slime, a row of little
pictures, a staff cut in two -- which nobody reports as a bug because it looks
like the game's own art. So each is put to the implementation it replaces over
the same pixels.
*/

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

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

// writePNG puts an image where the Python can read it.
func writePNG(t *testing.T, img image.Image) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.png")
	f, err := os.Create(path) //nolint:gosec // a path this test made
	require.NoError(t, err)
	require.NoError(t, png.Encode(f, img))
	require.NoError(t, f.Close())
	return path
}

/*
askPython hands one image over and gets back what the other implementation made
of it.

Compared by hash rather than by looking at the picture, because what matters is
that the two produce the same pixels -- and because "the same pixels" is the
only claim worth making about an icon.
*/
func askPython(t *testing.T, path, call string) shape {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import hashlib, json, os, sys
sys.path.insert(0, os.getcwd())
from PIL import Image
from terrariabonker import sprites
img = Image.open(`+quote(path)+`).convert("RGBA")
out = `+call+`
out = out.convert("RGBA")
print(json.dumps({"size": "%dx%d" % out.size,
                  "sum": hashlib.sha256(out.tobytes()).hexdigest()[:16]}))
`) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	raw, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(raw), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", raw)

	var got shape
	require.NoError(t, json.Unmarshal(raw, &got))
	return got
}

/*
realHome is the home directory before a test moved it.

The Python child works out where its own packages are from HOME at startup, so
a test that points HOME at a scratch directory and then asks the other
implementation a question gets "not importable" and a silent skip instead of an
answer.
*/
var realHome = os.Getenv("HOME")

// pyScript asks the Python something that is not about an image.
func pyScript(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", //nolint:gosec // a generated fixture
		"import json, os, sys\nsys.path.insert(0, os.getcwd())\n"+script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	raw, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(raw), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", raw)
	require.NoError(t, json.Unmarshal(raw, into))
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// De-animating a strip takes its first frame, and leaves everything else alone.
func TestDeanimateMatchesThePython(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"a four-frame strip", strip(10, 12, 4, 10)},
		{"a two-frame strip", strip(16, 20, 2, 18)},
		{"frames that fill their slots", strip(8, 8, 5, 8)},
		{"one tall block, like a staff", blob(10, 30, 0, 5, 10, 25)},
		{"wider than it is tall", blob(32, 20, 0, 0, 32, 20)},
		/*
			Two even bands in a wide image, which is not a strip.

			Without the shape test this is exactly what a strip looks like from
			the inside, and it would be cropped in half -- a sword with a
			separate hilt, cut at the grip.
		*/
		{"two bands in a wide image", bands(32, 20, 2, 8)},
		// And a strip whose height does not divide by the blocks in it.
		{"blocks that do not divide the height", bands(10, 25, 2, 8)},
		{"a strip whose blocks straddle their slots", strip(10, 12, 3, 14)},
		/*
			Three separate blocks that do not line up with their frames.

			The count divides the height and the blocks are distinct, so
			everything but their positions says "strip" -- and cropping to the
			first twelve rows would cut the second block in half.
		*/
		{"blocks that sit across the frame lines", blocksAt(10, 36,
			[][2]int{{0, 6}, {10, 16}, {24, 30}})},
		{"nothing in it at all", image.NewNRGBA(image.Rect(0, 0, 10, 40))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writePNG(t, tc.img)
			require.Equal(t, askPython(t, path, "sprites._deanimate(img)"),
				describe(t, sprites.Deanimate(tc.img)),
				"the two cropped it differently")
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
func TestFirstFrameMatchesThePython(t *testing.T) {
	for _, tc := range []struct {
		name   string
		img    image.Image
		frames int32
	}{
		{"a two-frame vertical strip", strip(32, 26, 2, 24), 2},
		{"one frame, left alone", blob(573, 804, 20, 20, 500, 700), 1},
		{"sixteen frames in two columns", grid(30, 40, 2, 8, 2), 16},
		{"a three by three grid", grid(24, 24, 3, 3, 4), 9},
		{"five columns", grid(20, 30, 5, 2, 2), 10},
		{"a count that does not divide the height", strip(16, 20, 3, 18), 7},
		{"frames too short to be frames", strip(16, 2, 8, 2), 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writePNG(t, tc.img)
			call := "sprites._first_frame(img, " + itoa(int(tc.frames)) + ")"
			require.Equal(t, askPython(t, path, call),
				describe(t, sprites.FirstFrame(tc.img, tc.frames)),
				"the two cropped it differently")
		})
	}
}

/*
A grid is cropped to its top-left cell, and one sprite with a detached piece is
not.

That second case is what the evenness rule exists for: the blocks of a grid are
the same size and the pieces of one sprite are not.
*/
func TestFirstGridCellMatchesThePython(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"a three by three grid", grid(24, 24, 3, 3, 4)},
		{"two columns", grid(30, 40, 2, 1, 6)},
		{"one sprite with a detached piece", detached()},
		{"one solid block", blob(40, 40, 0, 0, 40, 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writePNG(t, tc.img)
			require.Equal(t, askPython(t, path, "sprites._first_grid_cell(img)"),
				describe(t, sprites.FirstGridCell(tc.img)),
				"the two cropped it differently")
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
func TestCompositeChestMatchesThePython(t *testing.T) {
	sheet := grid(16, 16, 8, 6, 2)
	path := writePNG(t, sheet)
	for _, style := range []int{0, 1, 3, 7} {
		t.Run("style "+itoa(style), func(t *testing.T) {
			call := "sprites._composite_chest(img, " + itoa(style) + ")"
			require.Equal(t, askPython(t, path, call),
				describe(t, sprites.CompositeChest(sheet, style)),
				"the two assembled different chests")
		})
	}
}

/*
The tint is the game's own: the sheet is attenuated and the colour added on top.

Multiplying instead comes out far too dark to read at icon size, which is a
thing that looks like a rendering bug rather than a wrong formula.
*/
func TestTintedMatchesThePython(t *testing.T) {
	sheet := grid(12, 12, 2, 2, 2)
	path := writePNG(t, sheet)
	for _, tint := range [][4]byte{
		{0, 80, 255, 100}, {102, 204, 106, 255}, {0, 0, 0, 0}, {255, 255, 255, 255},
	} {
		name := itoa(int(tint[0])) + "," + itoa(int(tint[1])) + "," + itoa(int(tint[2]))
		t.Run(name, func(t *testing.T) {
			call := "sprites._tinted(img, [" + itoa(int(tint[0])) + ", " +
				itoa(int(tint[1])) + ", " + itoa(int(tint[2])) + ", " +
				itoa(int(tint[3])) + "])"
			require.Equal(t, askPython(t, path, call),
				describe(t, sprites.Tinted(sheet, tint)),
				"the two painted it differently")
		})
	}
}
