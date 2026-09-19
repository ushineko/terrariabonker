package sprites_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
The draw data is a file two programs share, so its shape is the contract.

The extractor runs unprivileged and the scan that produces this runs as root, so
nothing but the file connects them -- which makes the JSON the interface, and a
key spelled differently at either end a silent loss of every tinted icon. So the
file itself is written out below, not just the round trip through it.
*/

var (
	frames = map[int32]int32{1: 2, 4: 15, 245: 1}
	tints  = map[int32]sprites.NPCTint{
		1:  {Type: 1, Color: [4]byte{0, 80, 255, 100}},
		-3: {Type: 1, Color: [4]byte{102, 204, 106, 255}},
	}
)

/*
What is written is the file the extractor reads, key for key.

The shape is the contract between two programs, so it is spelled out here rather
than checked by writing and reading it back -- which would pass with any pair of
names as long as they matched.
*/
func TestTheFileIsTheAgreedShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, sprites.SaveNPCDrawData(frames, tints))

	raw, err := os.ReadFile(sprites.NPCFramesFile())
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, map[string]any{
		"frames": map[string]any{"1": 2.0, "4": 15.0, "245": 1.0},
		"tints": map[string]any{
			"1":  map[string]any{"type": 1.0, "color": []any{0.0, 80.0, 255.0, 100.0}},
			"-3": map[string]any{"type": 1.0, "color": []any{102.0, 204.0, 106.0, 255.0}},
		},
	}, got, "the file is a different shape")
}

// And it reads back as what went in, including the negative netID a variant is
// keyed by.
func TestTheFileIsReadBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, sprites.SaveNPCDrawData(frames, tints))

	gotFrames, gotTints := sprites.LoadNPCDrawData()
	require.Equal(t, frames, gotFrames, "different frame counts came back")
	require.Equal(t, tints, gotTints, "different tints came back")
	require.Contains(t, gotTints, int32(-3), "the negative netID did not survive")
}

/*
The file sits beside the icon cache rather than in the config directory.

Extraction runs unprivileged, and the config directory can end up root-owned
from a sudo memory command -- which would make this unwritable by the side that
has to read it.
*/
func TestTheFilePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Equal(t, filepath.Join(home, ".cache", "terrariabonker", "npcframes.json"),
		sprites.NPCFramesFile())
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
