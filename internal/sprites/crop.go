package sprites

import (
	"image"
	"image/color"
)

/*
Cropping a sheet down to one picture.

Sprites ship as sheets: an animated item is a tall strip of frames, an NPC is a
strip or a grid, and a naive decode shows the lot. Two rules do the cropping,
and which one applies is the difference between a guess and a measurement.
*/

const (
	// MinFramePx is the shortest a frame can be before the count is not
	// describing this sheet.
	MinFramePx = 4
	/*
		GridEvenness is how unequal blocks may be before this stops believing
		they are a grid.

		What it rejects is one sprite with a detached piece: those blocks differ
		in size, where a grid's do not. Measured over all 697 NPC sheets, the
		grid rule changes 19 and leaves 678 alone.
	*/
	GridEvenness = 1.35
	// TintKeep is how much of the neutral sheet survives before the tint goes
	// on top.
	TintKeep = 0.45
)

/*
Deanimate reduces an animated item's sheet to its first frame.

A guess, deliberately: items carry no frame count, so a strip is inferred from
being at least twice as tall as it is wide and split into evenly spaced blocks
of content. A single-frame item -- even a tall one like a staff -- has one block
and comes back untouched.
*/
func Deanimate(img image.Image) image.Image {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if h < 2*w {
		return img
	}
	blocks := contentRuns(rowsWithContent(img))
	n := len(blocks)
	if n < 2 || h%n != 0 {
		return img
	}
	frame := h / n
	for k, b := range blocks {
		// Each block has to sit inside its own frame slot, or this is not a
		// strip of equal frames and cropping it would cut a sprite in half.
		if k*frame > b[0] || b[1] >= (k+1)*frame {
			return img
		}
	}
	return crop(img, image.Rect(0, 0, w, frame))
}

/*
FirstFrame crops an NPC sheet to its first frame, using the game's own count.

Exact where Deanimate is a guess: that one infers a strip from the shape, which
is right for tall item strips and wrong for wide NPCs -- a two-frame Blue Slime
sheet is 32x52 and a one-frame Moon Lord is 573x804.

**The count counts frames, not rows.** Where a sheet is laid out in columns as
well, the rows are frames divided by columns: Queen Slime's sixteen frames are
two columns of eight, so dividing the height by sixteen yields half a slime. So
columns are counted first and the height divided by the rows that implies.

The division is floored rather than required to be exact: several sheets carry a
few rows of padding -- Duke Fishron is 1298 tall over 8 frames -- and refusing
those left the whole strip on screen.
*/
func FirstFrame(img image.Image, frames int32) image.Image {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	n := int(frames)
	if cols := evenBlocks(img, byColumn); cols != nil && n%len(cols) == 0 &&
		h/(n/len(cols)) >= MinFramePx {
		first := cols[0]
		return crop(img, image.Rect(first[0], 0, first[1]+1, h/(n/len(cols))))
	}
	if n >= 2 && h/n >= MinFramePx {
		img = crop(img, image.Rect(0, 0, w, h/n))
	}
	return FirstGridCell(img)
}

/*
FirstGridCell crops a grid sheet to its top-left cell.

Not every sheet is a vertical strip. Some lay frames out in columns too, and the
frame count says nothing about that -- so a vertical crop alone leaves a row of
little pictures.
*/
func FirstGridCell(img image.Image) image.Image {
	for _, axis := range []axis{byColumn, byRow} {
		blocks := evenBlocks(img, axis)
		if blocks == nil {
			continue // one sprite with detached parts, not a grid
		}
		lo, hi := blocks[0][0], blocks[0][1]
		w, h := img.Bounds().Dx(), img.Bounds().Dy()
		if axis == byColumn {
			img = crop(img, image.Rect(lo, 0, hi+1, h))
		} else {
			img = crop(img, image.Rect(0, lo, w, hi+1))
		}
	}
	return img
}

// axis says which way the blocks run.
type axis int

const (
	byColumn axis = iota
	byRow
)

/*
evenBlocks is the evenly sized runs of content along an axis, or nothing when
this is not a grid.
*/
func evenBlocks(img image.Image, along axis) [][2]int {
	var mask []bool
	if along == byColumn {
		mask = emptyColumns(img)
	} else {
		mask = emptyRows(img)
	}
	blocks := contentRuns(invert(mask))
	if len(blocks) < 2 {
		return nil
	}
	lo, hi := blocks[0][1]-blocks[0][0]+1, blocks[0][1]-blocks[0][0]+1
	for _, b := range blocks {
		size := b[1] - b[0] + 1
		lo = min(lo, size)
		hi = max(hi, size)
	}
	if float64(hi) > float64(lo)*GridEvenness {
		return nil
	}
	return blocks
}

// rowsWithContent is true for each row holding any pixel at all.
func rowsWithContent(img image.Image) []bool {
	return invert(emptyRows(img))
}

// emptyRows and emptyColumns are true where a whole line is transparent.
func emptyRows(img image.Image) []bool {
	b := img.Bounds()
	out := make([]bool, b.Dy())
	for y := range out {
		out[y] = true
		for x := range b.Dx() {
			if opaque(img, b.Min.X+x, b.Min.Y+y) {
				out[y] = false
				break
			}
		}
	}
	return out
}

func emptyColumns(img image.Image) []bool {
	b := img.Bounds()
	out := make([]bool, b.Dx())
	for x := range out {
		out[x] = true
		for y := range b.Dy() {
			if opaque(img, b.Min.X+x, b.Min.Y+y) {
				out[x] = false
				break
			}
		}
	}
	return out
}

func opaque(img image.Image, x, y int) bool {
	if n, ok := img.(*image.NRGBA); ok {
		return n.Pix[n.PixOffset(x, y)+3] > 0
	}
	_, _, _, a := img.At(x, y).RGBA()
	return a > 0
}

func invert(mask []bool) []bool {
	out := make([]bool, len(mask))
	for i, v := range mask {
		out[i] = !v
	}
	return out
}

// contentRuns is the runs of true in a mask, as inclusive [start, end] pairs.
func contentRuns(mask []bool) [][2]int {
	var out [][2]int
	start := -1
	for i, v := range mask {
		switch {
		case v && start < 0:
			start = i
		case !v && start >= 0:
			out = append(out, [2]int{start, i - 1})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(mask) - 1})
	}
	return out
}

// crop is a copy of a region, so the result does not hold the whole sheet.
func crop(img image.Image, r image.Rectangle) *image.NRGBA {
	r = r.Add(img.Bounds().Min)
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := range r.Dy() {
		for x := range r.Dx() {
			out.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out
}

/*
Tinted paints a neutral sheet with the game's own colour.

Terraria attenuates the lit texture and *adds* the NPC's colour on top, which is
why a Blue Slime's grey gel comes out blue. Reproduced closely enough for a
forty-pixel icon: multiplying instead comes out far too dark to read at that
size.
*/
func Tinted(img image.Image, tint [4]byte) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			src := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out.SetNRGBA(x, y, color.NRGBA{
				R: paint(src.R, tint[0]), G: paint(src.G, tint[1]),
				B: paint(src.B, tint[2]), A: src.A,
			})
		}
	}
	return out
}

// paint is one channel: what survives of the sheet, plus the tint.
func paint(have, add byte) byte {
	v := float64(have)*TintKeep + float64(add)
	if v > 255 {
		v = 255
	}
	if v < 0 {
		v = 0
	}
	return byte(v)
}
