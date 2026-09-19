package xnb_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/xnb"
)

/*
Decoding the game's own sprites, against the game's own sprites.

There is no synthetic fixture worth having here. LZX is a real compression with
three block types, a sliding window carried between frames and a Huffman tree
coded against another Huffman tree -- a hand-written stream would exercise
whichever paths the person writing it thought of. Fourteen thousand real files
exercise all of them, and the answer is checkable because another implementation
of the same format is right there.

Skipped where the game is not installed. That makes this a test that does not
run on every machine, which is worth saying plainly: it is the only thing
standing between a subtle decoder bug and a cache full of wrong pictures.
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

// decoded is one file's size and the hash of its pixels, which is how the two
// are compared without moving megabytes between them.
type decoded struct {
	Size string `json:"size"`
	Sum  string `json:"sum"`
	Err  string `json:"err"`
}

// The two decoders produce the same pixels, file for file.
func TestDecodingTheGamesSpritesMatchesThePython(t *testing.T) {
	dir := contentDir(t)
	names := spread(spriteNames(t, dir), sample)
	require.NotEmpty(t, names, "the content directory has no sprites in it")

	want := pythonDecodes(t, dir, names)
	require.Len(t, want, len(names))

	same := 0
	for i, name := range names {
		got := goDecode(filepath.Join(dir, name))
		require.Equalf(t, want[i], got, "%s decoded differently", name)
		if got.Err == "" {
			same++
		}
	}
	require.Positivef(t, same, "every one of the %d sprites failed to decode", len(names))
	/*
		And most of them decoded, rather than the two agreeing that nothing
		works. A comparison between two broken readers passes perfectly.
	*/
	require.Greater(t, same, len(names)*9/10,
		"only %d of %d sprites decoded at all", same, len(names))
}

// goDecode is this package's answer for one file.
func goDecode(path string) decoded {
	img, err := xnb.ReadTexture(path)
	if err != nil {
		return decoded{Err: "failed"}
	}
	sum := sha256.Sum256(img.Pix)
	return decoded{
		Size: fmt.Sprintf("%dx%d", img.Rect.Dx(), img.Rect.Dy()),
		Sum:  hex.EncodeToString(sum[:8]),
	}
}

// pythonDecodes is the other implementation's answer for the same files.
func pythonDecodes(t *testing.T, dir string, names []string) []decoded {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import hashlib, json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import xnb
req = json.loads(sys.stdin.read())
out = []
for name in req["names"]:
    try:
        img = xnb.read_item_texture(os.path.join(req["dir"], name))
    except Exception:
        out.append({"size": "", "sum": "", "err": "failed"})
        continue
    out.append({"size": "%dx%d" % (img.width, img.height),
                "sum": hashlib.sha256(img.tobytes()).hexdigest()[:16],
                "err": ""})
print(json.dumps(out))
`)
	cmd.Dir = repoRoot
	raw, err := json.Marshal(map[string]any{"dir": dir, "names": names})
	require.NoError(t, err)
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", firstLines(string(out)))

	var got []decoded
	require.NoError(t, json.Unmarshal(out, &got))
	return got
}

func firstLines(s string) string {
	lines := strings.SplitN(s, "\n", 6)
	return strings.Join(lines, "\n")
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
