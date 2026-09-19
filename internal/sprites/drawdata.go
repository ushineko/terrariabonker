/*
Package sprites is the icon side of the catalog: what the extractor needs from
the game's memory, written where an unprivileged process can read it.

Extraction itself runs without sudo -- it reads the game's own asset files -- but
two of the things it needs are only in the running process: how many animation
frames each NPC's sheet holds, and the tint the game paints a neutral sheet
with. So the privileged side writes them out and the extractor reads them back.

Ported from terrariabonker/sprites.py (spec 051, step 5).
*/
package sprites

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

/*
NPCTint is one variant's colour and the sheet it is painted onto.

Keyed by netID rather than by type on purpose: every coloured slime shares one
neutral sheet and differs only by this colour, so a tinted variant cannot reuse
the type's icon and needs its own.
*/
type NPCTint struct {
	Type  int32   `json:"type"`
	Color [4]byte `json:"color"`
}

// NPCDrawData is the file's shape: frame counts by NPC type, tints by netID.
type NPCDrawData struct {
	Frames map[string]int32   `json:"frames"`
	Tints  map[string]NPCTint `json:"tints"`
}

// NPCFramesFile is where the two live, beside the icon cache rather than in the
// config directory, which can be root-owned.
func NPCFramesFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "terrariabonker", "npcframes.json")
}

/*
SaveNPCDrawData writes the frame counts and tints out. Best effort.

Written to a temporary file and renamed, so a reader never sees half of one --
the extractor runs unprivileged and concurrently with the scan that produces
this.
*/
func SaveNPCDrawData(frames map[int32]int32, tints map[int32]NPCTint) error {
	path := NPCFramesFile()
	if path == "" {
		return os.ErrNotExist
	}
	data := NPCDrawData{Frames: map[string]int32{}, Tints: map[string]NPCTint{}}
	for k, v := range frames {
		data.Frames[strconv.Itoa(int(k))] = v
	}
	for k, v := range tints {
		data.Tints[strconv.Itoa(int(k))] = v
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode the draw data: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("make the cache directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil { //nolint:gosec // read by an unprivileged extractor
		return fmt.Errorf("write the draw data: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("put the draw data in place: %w", err)
	}
	return nil
}

// LoadNPCDrawData reads them back, or gives empty maps when there is no file.
func LoadNPCDrawData() (map[int32]int32, map[int32]NPCTint) {
	frames, tints := map[int32]int32{}, map[int32]NPCTint{}
	path := NPCFramesFile()
	if path == "" {
		return frames, tints
	}
	blob, err := os.ReadFile(path) //nolint:gosec // a path this package owns
	if err != nil {
		return frames, tints
	}
	var data NPCDrawData
	if err := json.Unmarshal(blob, &data); err != nil {
		return frames, tints
	}
	for k, v := range data.Frames {
		if id, err := strconv.Atoi(k); err == nil {
			frames[int32(id)] = v //nolint:gosec // an NPC type
		}
	}
	for k, v := range data.Tints {
		if id, err := strconv.Atoi(k); err == nil {
			tints[int32(id)] = v //nolint:gosec // an NPC netID
		}
	}
	return frames, tints
}
