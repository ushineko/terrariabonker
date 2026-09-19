/*
Command terrariabonker-gui is the desktop control panel.

It drives the terrariabonker CLI and does nothing to the game itself: every
operation is a subprocess under sudo, because memory access needs root and a
window must not run as root. See AGENTS.md's architecture rule and spec 050.

	terrariabonker-gui                     open on the first section
	terrariabonker-gui --section Inventory open on one directly
	terrariabonker-gui --scheme "Breeze Dark"
	terrariabonker-gui --cli /path/to/terrariabonker  drive a CLI that is not on PATH
	terrariabonker-gui --pprof 127.0.0.1:6060         serve a profile while debugging
*/
package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // served only on the address --pprof names
	"os"
	"strings"
	"time"

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
		pprofAt = flag.String("pprof", "", "serve net/http/pprof on this address (debugging)")
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

	// Off unless asked for, and bound to whatever address the flag names, which
	// is a loopback one in every use it has had: this window is unprivileged
	// but it can still see the game's item catalog.
	if *pprofAt != "" {
		go func() {
			srv := &http.Server{Addr: *pprofAt, ReadHeaderTimeout: 5 * time.Second}
			fmt.Fprintln(os.Stderr, "pprof on http://"+*pprofAt+"/debug/pprof/")
			if err := srv.ListenAndServe(); err != nil {
				fmt.Fprintln(os.Stderr, "pprof: "+err.Error())
			}
		}()
	}

	gui.Run(gui.Options{
		Version: version,
		Section: *section,
		Scheme:  *scheme,
		CLI:     *cli,
	})
}
