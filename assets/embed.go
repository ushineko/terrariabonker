/*
Package assets carries the files the Go binaries embed.

The package lives in the assets directory so that go:embed can reach the icon
where it already is. An embed directive cannot name a path outside its own
package, and the alternative -- a second copy of the SVG under internal/ --
would be exactly the duplicate spelling this project has a rule against.
*/
package assets

import _ "embed"

// IconSVG is the application icon: the grass-topped dirt block. The desktop
// entry points at this same file on disk.
//
//go:embed terrariabonker.svg
var IconSVG []byte
