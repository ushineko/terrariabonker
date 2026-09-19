package sprites

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/version"
	"github.com/ushineko/terrariabonker/internal/xnb"
)

/*
Building the icon cache out of the game's own files.

Unprivileged throughout: it reads Content/Images and writes under ~/.cache.
Nothing here touches the game's memory -- the two things that can only come from
there, the frame counts and the tints, are handed over in a file by the side
that can read them.
*/

// ErrNoContent is what an extraction reports when it cannot find the game's
// files.
var ErrNoContent = errors.New(
	"cannot find Terraria's Content/Images -- launch the game once so its path " +
		"can be learned, then extract")

// Result is what an extraction managed.
type Result struct {
	OK     int
	Failed int
	Total  int
}

// Options are what to extract and where from.
type Options struct {
	// ExePath is the running game's executable, used only to learn where its
	// content lives. Empty is fine once that has been learned.
	ExePath string
	// Version keys the cache. Empty means the build this program was written
	// against.
	Version string
	// ItemIDs is what to extract; nil means every item there is a name for.
	ItemIDs []int
	// Force re-decodes icons that are already cached.
	Force bool
	// Progress is called as it goes, with how many of how many.
	Progress func(done, total int)
}

/*
Extract decodes the sprites into the cache. Idempotent: an icon already there is
kept unless Force says otherwise.

A cache from an older scope is rebuilt rather than skipped -- its icons may be
wrong, not merely fewer.
*/
func Extract(o Options) (Result, error) {
	src := ContentImagesDir(o.ExePath)
	if src == "" {
		return Result{}, ErrNoContent
	}
	v := o.Version
	if v == "" {
		v = version.KnownVersion
	}
	ids := o.ItemIDs
	if ids == nil {
		var err error
		if ids, err = AllItemIDs(); err != nil {
			return Result{}, err
		}
	}
	sort.Ints(ids)

	/*
		No frame counts means no way to crop a sheet to one frame, so the NPCs
		are skipped rather than cached as whole strips: a wrong icon persists
		until the scope is bumped, while a missing one is fixed by the next run.
		The counts come from the privileged side, which may not have run yet.
	*/
	frames, tints := LoadNPCDrawData()
	npcTypes := make([]int32, 0, len(frames))
	for t := range frames {
		npcTypes = append(npcTypes, t)
	}
	sort.Slice(npcTypes, func(i, j int) bool { return npcTypes[i] < npcTypes[j] })

	dir := CacheDir(v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("make the icon cache: %w", err)
	}
	refresh := o.Force || !IsCached(v)

	book, err := game.CraftingBook()
	if err != nil {
		return Result{}, err
	}
	sheets := map[int]image.Image{}
	res := Result{Total: len(ids) + len(npcTypes)}

	for n, id := range ids {
		dst := IconPath(id, v)
		if !refresh && exists(dst) {
			res.OK++
		} else if img := itemIcon(src, id, book, sheets); img != nil && save(dst, img) == nil {
			res.OK++
		} else {
			res.Failed++
		}
		if o.Progress != nil && n%100 == 0 {
			o.Progress(n+1, res.Total)
		}
	}

	for n, npcType := range npcTypes {
		dst := NPCIconPath(npcType, v)
		if !refresh && exists(dst) {
			res.OK++
		} else if img := npcIcon(src, npcType, frames[npcType]); img != nil &&
			save(dst, img) == nil {
			res.OK++
		} else {
			res.Failed++
		}
		if o.Progress != nil && n%50 == 0 {
			o.Progress(len(ids)+n+1, res.Total)
		}
	}

	/*
		And the tinted variants. Every coloured slime shares one neutral sheet
		and differs only by its colour, which is per netID -- so each needs its
		own icon rather than the type's.
	*/
	tinted := 0
	for _, netID := range sortedTints(tints) {
		spec := tints[netID]
		dst := NPCTintedIconPath(netID, v)
		if !refresh && exists(dst) {
			tinted++
			continue
		}
		base, err := loadPNG(NPCIconPath(spec.Type, v))
		if err != nil {
			continue // the type's own icon is not there; nothing to tint
		}
		// A tint that cannot be written is not a failure: the type's icon is
		// still on screen, in the wrong colour.
		if save(dst, Tinted(base, spec.Color)) == nil {
			tinted++
		}
	}

	writeDone(dir, Done{
		Version: v, Scope: Scope, OK: res.OK, Failed: res.Failed,
		Total: res.Total, NPCs: len(npcTypes), Tinted: tinted,
	})
	if o.Progress != nil {
		o.Progress(res.Total, res.Total)
	}
	return res, nil
}

/*
itemIcon is one item's picture: its own sprite where it has one, and the tile it
places where it does not.

A trapped chest has no sprite file at all, and drawing it from the tile sheet is
the difference between an icon and a blank square in the recipe book.
*/
func itemIcon(src string, id int, book *game.Recipes, sheets map[int]image.Image) image.Image {
	path := filepath.Join(src, fmt.Sprintf("Item_%d.xnb", id))
	if exists(path) {
		if raw, err := xnb.ReadTexture(path); err == nil {
			return Deanimate(raw)
		}
	}
	icon, known := book.TileIcons[fmt.Sprint(id)]
	if !known || len(icon) < 2 {
		return nil
	}
	return tileIcon(src, icon[0], icon[1], sheets)
}

// npcIcon is one NPC sheet cropped to its first frame.
func npcIcon(src string, npcType, frames int32) image.Image {
	path := filepath.Join(src, fmt.Sprintf("NPC_%d.xnb", npcType))
	if !exists(path) {
		return nil
	}
	raw, err := xnb.ReadTexture(path)
	if err != nil {
		return nil
	}
	return FirstFrame(raw, frames)
}

/*
tileIcon renders a placeable item that has no sprite of its own, from the tile
sheet it places.

Tiles are sixteen pixels with two of padding, so eighteen apart; a chest is two
by two of them, thirty-six. Styles run left to right and wrap by the sheet's
width, with a row pitch of thirty-eight. The four tiles are composited
adjacently, which drops the internal padding and makes the chest seamless.
*/
func tileIcon(src string, tile, style int, sheets map[int]image.Image) image.Image {
	sheet, tried := sheets[tile]
	if !tried {
		path := filepath.Join(src, fmt.Sprintf("Tiles_%d.xnb", tile))
		if exists(path) {
			if raw, err := xnb.ReadTexture(path); err == nil {
				sheet = raw
			}
		}
		sheets[tile] = sheet
	}
	if sheet == nil {
		return nil
	}
	return CompositeChest(sheet, style)
}

// ChestTile is the pitch of the chest grid within a tile sheet, and of one tile
// inside it.
const (
	ChestPitchX = 36
	ChestPitchY = 38
	TilePitch   = 18
	TileSize    = 16
)

// CompositeChest assembles the two-by-two chest at a style into a 32x32 icon.
func CompositeChest(sheet image.Image, style int) *image.NRGBA {
	b := sheet.Bounds()
	perRow := max(1, b.Dx()/ChestPitchX)
	fx := (style % perRow) * ChestPitchX
	fy := (style / perRow) * ChestPitchY

	out := image.NewNRGBA(image.Rect(0, 0, 2*TileSize, 2*TileSize))
	for _, at := range [4][4]int{
		{0, 0, 0, 0}, {TilePitch, 0, TileSize, 0},
		{0, TilePitch, 0, TileSize}, {TilePitch, TilePitch, TileSize, TileSize},
	} {
		box := image.Rect(fx+at[0], fy+at[1], fx+at[0]+TileSize, fy+at[1]+TileSize).
			Add(b.Min)
		if box.Max.X > b.Max.X || box.Max.Y > b.Max.Y {
			continue
		}
		draw.Draw(out, image.Rect(at[2], at[3], at[2]+TileSize, at[3]+TileSize),
			sheet, box.Min, draw.Over)
	}
	return out
}

// AllItemIDs is every item there is a name for, plus everything a recipe
// mentions: the inventory can hold anything, not only what can be crafted.
func AllItemIDs() ([]int, error) {
	names, err := game.ItemNames()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for id := range names.All() {
		seen[id] = true
	}
	book, err := game.CraftingBook()
	if err != nil {
		return nil, err
	}
	for _, r := range book.Recipes {
		seen[r.Out] = true
		for _, pair := range r.Ing {
			if len(pair) > 0 {
				seen[pair[0]] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// sortedTints is the tinted netIDs in order, so a run does the same thing
// twice.
func sortedTints(tints map[int32]NPCTint) []int32 {
	out := make([]int32, 0, len(tints))
	for id := range tints {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// save writes one icon.
func save(path string, img image.Image) error {
	f, err := os.Create(path) //nolint:gosec // a path in this package's own cache
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, img)
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path) //nolint:gosec // a path in this package's own cache
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return png.Decode(f)
}

// writeDone leaves the marker that says what this cache holds. Best effort: a
// marker that cannot be written means the next run extracts again.
func writeDone(dir string, done Done) {
	raw, err := json.Marshal(done)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, ".done"), raw, 0o644) //nolint:gosec // the user's own cache
}
