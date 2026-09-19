/*
Command terrariabonker-gui is the desktop control panel.

It drives the terrariabonker CLI and does nothing to the game itself: every
operation is a subprocess under sudo, because memory access needs root and a
window must not run as root. See AGENTS.md's architecture rule and spec 050.

	terrariabonker-gui                     open on the first section
	terrariabonker-gui --section Inventory open on one directly
	terrariabonker-gui --scheme "Breeze Dark"
	terrariabonker-gui --cli ./terrariabonker.py   drive a CLI that is not on PATH
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ushineko/fynedesygn/theme"

	"github.com/ushineko/terrariabonker/internal/buildinfo"
	"github.com/ushineko/terrariabonker/internal/gui"
)

// version is the panel's version. It defaults to buildinfo's, which the
// Makefile stamps from .tag, so the two front ends carry the same number.
var version = buildinfo.Version

func main() {
	var (
		section = flag.String("section", "", "open on this section: "+strings.Join(gui.SectionNames(), ", "))
		scheme  = flag.String("scheme", "", "colour scheme for this run, not saved: "+strings.Join(theme.SchemeNames(), ", "))
		cli     = flag.String("cli", "", "path to the terrariabonker CLI (default: found on PATH)")
		showVer = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("terrariabonker-gui " + version)
		return
	}
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	gui.Run(gui.Options{
		Version: version,
		Section: *section,
		Scheme:  *scheme,
		CLI:     *cli,
	})
}
