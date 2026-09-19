/*
Command terrariabonker is the command line trainer.

Separate from cmd/terrariabonker-gui on purpose, and the separation is the
security boundary rather than a packaging choice. This binary runs as root: it
reads and writes another process's memory, which ptrace_scope=1 allows nobody
else. The window does not, and reaches memory only by running this one under
sudo -- so nothing with a window in it ever holds those privileges.

This one needs no display server, no OpenGL and no CGO; the window needs all
three. Neither imports the other's front end.
*/
package main

import (
	"context"
	"os"

	"github.com/ushineko/terrariabonker/internal/cli"
)

func main() {
	app := cli.NewApp(cli.Options{})
	os.Exit(app.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
