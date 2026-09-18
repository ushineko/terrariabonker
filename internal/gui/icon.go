package gui

import (
	"fyne.io/fyne/v2"

	"github.com/ushineko/terrariabonker/assets"
)

// appIcon is the window and application icon.
//
// Embedded rather than read from disk at start-up: the binary is installed into
// ~/.local/bin and the asset is not, so a window that looked for the file beside
// itself would come up iconless on every installed copy.
//
// The resource name matters. Fyne picks its renderer from the extension, so an
// SVG announced as anything else draws nothing.
func appIcon() fyne.Resource {
	return fyne.NewStaticResource("terrariabonker.svg", assets.IconSVG)
}
