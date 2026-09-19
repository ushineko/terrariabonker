/*
Package buildinfo carries what this binary was built from.

A package of its own so the -ldflags path points at program metadata rather
than at some feature package that happens to be convenient.

Copied from clockwork-orange (same author) -- keep in sync by hand.
*/
package buildinfo

// Version and Commit are injected at build time with -ldflags -X. The defaults
// are what a plain `go build` or `go test` produces.
var (
	Version = "dev"
	Commit  = "unknown"
)
