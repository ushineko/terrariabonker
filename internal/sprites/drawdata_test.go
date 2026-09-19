package sprites_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
The draw data is a file two programs share, so its shape is the contract.

The extractor runs unprivileged and the scan that produces this runs as root, so
nothing but the file connects them -- which makes the JSON the interface, and a
key spelled differently on either side a silent loss of every tinted icon. Both
implementations are asked to write it and then to read the other's.
*/

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// realHome is the home directory before the test moved it, so the Python child
// can still find its own packages.
var realHome = os.Getenv("HOME")

/*
pyDrawData runs a script against a chosen file, without moving HOME.

Pointing HOME at a scratch directory is how the file would naturally be
redirected, and it is what broke this: the child interpreter works out where the
user's packages are from HOME at startup, numpy goes missing, and the helper
reports "the Python is not importable" and *skips*. Three comparisons here
skipped in silence before the file was chosen this way instead.
*/
func pyDrawData(t *testing.T, at, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	prelude := `
import json, os, sys
sys.path.insert(0, os.getcwd())
from terrariabonker import sprites
`
	if at != "" {
		prelude += "sprites._NPC_FRAMES_FILE = " + strconv.Quote(at) + "\n"
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", prelude+script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	return string(out)
}

var (
	frames = map[int32]int32{1: 2, 4: 15, 245: 1}
	tints  = map[int32]sprites.NPCTint{
		1:  {Type: 1, Color: [4]byte{0, 80, 255, 100}},
		-3: {Type: 1, Color: [4]byte{102, 204, 106, 255}},
	}
)

// What this writes, the Python reads back as the same thing.
func TestThePythonReadsWhatThisWrote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, sprites.SaveNPCDrawData(frames, tints))

	out := pyDrawData(t, sprites.NPCFramesFile(), `
f, t = sprites.load_npc_draw_data()
print(json.dumps({"frames": {str(k): v for k, v in f.items()},
                  "tints": {str(k): v for k, v in t.items()}}))`)

	var got struct {
		Frames map[string]int32           `json:"frames"`
		Tints  map[string]sprites.NPCTint `json:"tints"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, map[string]int32{"1": 2, "4": 15, "245": 1}, got.Frames,
		"the Python read different frame counts")
	require.Equal(t, sprites.NPCTint{Type: 1, Color: [4]byte{0, 80, 255, 100}}, got.Tints["1"],
		"the Python read a different tint")
	require.Equal(t, sprites.NPCTint{Type: 1, Color: [4]byte{102, 204, 106, 255}},
		got.Tints["-3"], "the negative netID did not survive the round trip")
}

// And the other way round: what the Python writes, this reads back.
func TestThisReadsWhatThePythonWrote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pyDrawData(t, sprites.NPCFramesFile(), `
sprites.save_npc_draw_data({1: 2, 4: 15, 245: 1},
                           {1: {"type": 1, "color": [0, 80, 255, 100]},
                            -3: {"type": 1, "color": [102, 204, 106, 255]}})
print("{}")`)

	gotFrames, gotTints := sprites.LoadNPCDrawData()
	require.Equal(t, frames, gotFrames, "different frame counts came back")
	require.Equal(t, tints, gotTints, "different tints came back")
}

/*
The two agree on where the file goes, which is the other half of sharing one.

Asked of the real home directory rather than a scratch one, because that is the
only home both can be asked about: the child cannot be pointed somewhere else
without losing its own packages.
*/
func TestTheFilePathMatchesThePython(t *testing.T) {
	out := pyDrawData(t, "", `print(json.dumps(sprites._NPC_FRAMES_FILE))`)

	var want string
	require.NoError(t, json.Unmarshal([]byte(out), &want))
	t.Setenv("HOME", realHome)
	require.Equal(t, want, sprites.NPCFramesFile())
}

// Nothing there is not a failure: the extractor runs before the first scan has
// ever happened, and has to draw what it can.
func TestNoDrawDataAtAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gotFrames, gotTints := sprites.LoadNPCDrawData()
	require.Empty(t, gotFrames)
	require.Empty(t, gotTints)
}

/*
A half-written file is refused rather than half-believed.

The damage here is a frame count that is not a number, rather than a truncation:
a decoder rejects a truncated file before it fills anything, but it fills what it
has already read before it reaches a value of the wrong type. So ignoring the
error leaves a catalog that is short rather than absent -- and an icon set
missing its last few NPCs looks like the game not having them.
*/
func TestGarbageDrawData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cache", "terrariabonker")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "npcframes.json"),
		[]byte(`{"frames": {"1": 2, "4": "fifteen"}}`), 0o600))

	gotFrames, gotTints := sprites.LoadNPCDrawData()
	require.Empty(t, gotFrames)
	require.Empty(t, gotTints)
}
