package xnb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/xnb"
)

/*
Decoding the game's own sprites.

There is no synthetic fixture worth having for the decoder. LZX is a real
compression with three block types, a sliding window carried between frames and
a Huffman tree coded against another Huffman tree -- a hand-written stream would
exercise whichever paths the person writing it thought of. The game's own files
exercise all of them.

The pixels were checked once, against the implementation this was ported from,
over all 14,015 sprites the game ships: every one decoded identically. That
cannot be kept as a test, because the art changes when the game updates and a
frozen digest would then fail for a reason that is not a bug. What is kept is
that they all still decode, which is what a broken decoder stops doing.

Skipped where the game is not installed. That makes this a test that does not
run on every machine, which is worth saying plainly.
*/

// contentDir is where the game's sprites are, or nothing.
func contentDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to look in")
	}
	raw, err := os.ReadFile(filepath.Join(home, ".cache", "terrariabonker", "paths.json"))
	if err != nil {
		t.Skip("the game's content directory has not been learned on this machine")
	}
	var paths struct {
		ContentImages string `json:"content_images"`
	}
	if err := json.Unmarshal(raw, &paths); err != nil || paths.ContentImages == "" {
		t.Skip("the game's content directory has not been learned on this machine")
	}
	if _, err := os.Stat(paths.ContentImages); err != nil {
		t.Skip("the game's content directory is not there")
	}
	return paths.ContentImages
}

// sprites is the files in a directory, in order.
func spriteNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".xnb" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

/*
sample is how many of them to decode.

Every file takes about a third of a millisecond here and rather longer on the
other side, so the whole set is a comparison somebody runs deliberately rather
than one that runs on every save. The sample is spread across the directory by
taking every nth file, so it is item sprites, NPC sheets, tile sheets and the
odd thing in between rather than four hundred accessories.
*/
const sample = 400

func spread(names []string, want int) []string {
	if len(names) <= want {
		return names
	}
	step := len(names) / want
	out := make([]string, 0, want)
	for i := 0; i < len(names); i += step {
		out = append(out, names[i])
	}
	return out
}

/*
Every sprite the game ships decodes, and decodes into a plausible picture.

A decoder that broke would fail on most of them rather than on a few, so the
bar is set at nine in ten: a handful of unreadable files is the game shipping
something this does not read, and a hundred is a bug.
*/
func TestTheGamesSpritesDecode(t *testing.T) {
	dir := contentDir(t)
	names := spread(spriteNames(t, dir), sample)
	require.NotEmpty(t, names, "the content directory has no sprites in it")

	decoded, pixels := 0, 0
	for _, name := range names {
		img, err := xnb.ReadTexture(filepath.Join(dir, name))
		if err != nil {
			require.Truef(t, xnb.IsFormat(err), "%s failed for a reason that is not the format: %v", name, err)
			continue
		}
		decoded++
		w, h := img.Rect.Dx(), img.Rect.Dy()
		require.Positivef(t, w, "%s decoded to no width", name)
		require.Positivef(t, h, "%s decoded to no height", name)
		require.Lenf(t, img.Pix, w*h*4, "%s has the wrong number of pixels for its size", name)
		for _, b := range img.Pix {
			if b != 0 {
				pixels++
				break
			}
		}
	}
	require.Greaterf(t, decoded, len(names)*9/10,
		"only %d of %d sprites decoded at all", decoded, len(names))
	require.Greaterf(t, pixels, decoded*9/10,
		"only %d of %d decoded sprites have any pixels in them", pixels, decoded)
}

// A file that is not an XNB is refused rather than decoded into something.
func TestSomethingThatIsNotAnXNB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.xnb")
	require.NoError(t, os.WriteFile(path, []byte("this is not a sprite"), 0o600))

	_, err := xnb.ReadTexture(path)
	require.Error(t, err)
	require.True(t, xnb.IsFormat(err), "a wrong file was not reported as a wrong file")
}

/*
A truncated XNB is refused rather than decoded into half a picture.

Half a sprite is the failure that lasts: it lands in the cache and stays there,
where a refusal is retried on the next run.
*/
func TestATruncatedXNB(t *testing.T) {
	dir := contentDir(t)
	names := spriteNames(t, dir)
	require.NotEmpty(t, names)

	raw, err := os.ReadFile(filepath.Join(dir, names[0])) //nolint:gosec // the game's own file
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "short.xnb")
	require.NoError(t, os.WriteFile(path, raw[:len(raw)/2], 0o600))
	_, err = xnb.ReadTexture(path)
	require.Error(t, err, "half a file decoded")
	require.True(t, xnb.IsFormat(err))
}

// And a file this cannot read is a refusal, not a panic.
func TestGarbageDoesNotPanic(t *testing.T) {
	for _, body := range []string{
		"XNB", "XNBw\x05\x80", "XNBw\x05\x80\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00",
		"XNBw\x05\x00\x0e\x00\x00\x00junk",
	} {
		path := filepath.Join(t.TempDir(), "odd.xnb")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		require.NotPanics(t, func() { _, _ = xnb.ReadTexture(path) })
	}
}

/*
buildXNB writes an uncompressed XNB by hand.

The game ships nothing but SurfaceFormat.Color, so the refusals around the
texture header have nothing real to fire on. An uncompressed container is the
one part of the format simple enough to write out, and it is enough to put a
wrong number in the field and see what happens.
*/
func buildXNB(t *testing.T, format int32, width, height, dataLen int) string {
	t.Helper()
	var content []byte
	content = append(content, 1)          // one reader
	content = append(content, 4)          // the reader's name is four bytes
	content = append(content, "Tex2"...)  //
	content = append(content, 0, 0, 0, 0) // the reader's version
	content = append(content, 0)          // no shared resources
	content = append(content, 1)          // the primary asset uses reader 1

	put := func(v uint32) {
		content = append(content, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	put(uint32(format))  //nolint:gosec // a format, as its bits
	put(uint32(width))   //nolint:gosec // a size the test chose
	put(uint32(height))  //nolint:gosec // a size the test chose
	put(1)               // one mip
	put(uint32(dataLen)) //nolint:gosec // a size the test chose
	content = append(content, make([]byte, dataLen)...)

	raw := []byte{'X', 'N', 'B', 'w', 5, 0}
	size := uint32(10 + len(content)) //nolint:gosec // a small file
	raw = append(raw, byte(size), byte(size>>8), byte(size>>16), byte(size>>24))
	raw = append(raw, content...)

	path := filepath.Join(t.TempDir(), "made.xnb")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// A hand-made file decodes, which is what makes the refusals below mean
// something.
func TestAHandMadeXNBDecodes(t *testing.T) {
	img, err := xnb.ReadTexture(buildXNB(t, xnb.SurfaceColor, 2, 3, 2*3*4))
	require.NoError(t, err)
	require.Equal(t, 2, img.Rect.Dx())
	require.Equal(t, 3, img.Rect.Dy())
}

/*
A surface format this does not read is refused rather than reinterpreted.

Every file the game ships is SurfaceFormat.Color, so nothing real exercises
this -- and a compressed format read as raw colour is not an error, it is a
picture of noise that sits in the cache looking like a sprite.
*/
func TestAnUnknownSurfaceFormatIsRefused(t *testing.T) {
	for _, format := range []int32{1, 4, 28} {
		_, err := xnb.ReadTexture(buildXNB(t, format, 2, 3, 2*3*4))
		require.Errorf(t, err, "SurfaceFormat %d was decoded as colour", format)
		require.True(t, xnb.IsFormat(err))
	}
}

// And a texture whose data is shorter than its own size is refused.
func TestATextureShorterThanItsSize(t *testing.T) {
	_, err := xnb.ReadTexture(buildXNB(t, xnb.SurfaceColor, 8, 8, 16))
	require.Error(t, err, "a short texture was decoded")
	require.True(t, xnb.IsFormat(err))
}

// A texture with no pixels at all is refused rather than made into an empty
// image.
func TestATextureWithNoSize(t *testing.T) {
	for _, size := range [][2]int{{0, 4}, {4, 0}} {
		_, err := xnb.ReadTexture(buildXNB(t, xnb.SurfaceColor, size[0], size[1], 0))
		require.Errorf(t, err, "a %dx%d texture was decoded", size[0], size[1])
	}
}
