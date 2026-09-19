/*
Package cli is the command line front end.

A thin adapter over the service: every subcommand elevates once, attaches to the
running game, calls one operation and formats the result as text or --json. No
game logic lives here, so the command line and the window cannot drift -- and
--json is the contract the window consumes, which is why the argv surface is
kept flag for flag with what it replaced.

The window is a separate program and cannot call any of this in-process: it runs
unprivileged and memory needs root, so it shells out to this binary under sudo.
That is also why `serve` exists.

Ported from terrariabonker/cli.py (spec 051, step 6).
*/
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
	"github.com/ushineko/terrariabonker/internal/service"
)

// Exit codes: success, an operation that ran and failed, and a misuse of the
// command line, so a script can tell the last two apart.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// UsageError marks a failure caused by how the command was invoked.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usagef(format string, args ...any) error {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

/*
ExitError carries an exit code that is neither success nor an ordinary failure.

`version` uses it: an incompatible build is not a command that went wrong, it is
a report whose answer is "no", and a script wants to tell those apart.
*/
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

/*
Game is one attachment to the running game: the service, and the patcher over
the same memory.

They are built together because they are the same process seen two ways, and
because a test supplies both at once.
*/
type Game struct {
	Svc     *service.Service
	Patcher *patch.Patcher
	PID     int
}

// Options configure a command tree. The zero value is production.
type Options struct {
	// Attach opens the running game; nil means the real one.
	Attach func() (*Game, error)
	// Elevate acquires root; nil means the real re-exec under sudo.
	Elevate func() error
	/*
		Alive reports whether a pid is still there; nil means /proc.

		The worker drops its warm attachment when the game it was talking to has
		gone, and answering this is the only way to exercise that without
		starting and killing a process.
	*/
	Alive func(pid int) bool
}

// App is a built command tree and what it attached to.
type App struct {
	Root *cobra.Command

	attach  func() (*Game, error)
	elevate func() error
	alive   func(pid int) bool

	/*
		warm is an attachment kept between requests.

		Only `serve` sets it. Locating the player is nearly all the cost of a
		read -- a full memory scan -- so a one-shot run pays seconds where a warm
		request pays milliseconds, which is what makes the window's inventory
		sync affordable at one hertz.
	*/
	warm *Game
}

// NewApp builds the command tree.
func NewApp(o Options) *App {
	a := &App{attach: o.Attach, elevate: o.Elevate, alive: o.Alive}
	if a.attach == nil {
		a.attach = attachToGame
	}
	if a.elevate == nil {
		a.elevate = proc.Elevate
	}
	if a.alive == nil {
		a.alive = alive
	}
	root := &cobra.Command{
		Use:   "terrariabonker",
		Short: "From-scratch /proc memory trainer for Terraria (Proton/wine-mono)",
		Long: "terrariabonker reads and writes the running game's memory to edit the\n" +
			"player, the inventory and the world. With no arguments it reports what is\n" +
			"running; the window is a separate program, terrariabonker-gui.",
		// Failures are reported once, by Execute, with the program name.
		// Leaving cobra's own reporting on prints every one of them twice.
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		/*
			A word that is not a command is a misuse, which has its own exit
			code.

			Without this cobra reports it as an ordinary failure, and a script
			cannot tell a typo from an operation that ran and did not work --
			which is the whole reason the two codes are different.
		*/
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("%q is not a command", args[0])
			}
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &UsageError{Err: err} })
	a.Root = root
	root.AddCommand(a.commands()...)
	return a
}

// Root builds a production command tree, for the parity test.
func Root() *cobra.Command { return NewApp(Options{}).Root }

/*
Execute runs the tree and returns the process exit code.

SIGINT and SIGTERM cancel the context, which is how the blocking watch loops
end: they stop at the round they are in and print what they took, rather than
dying mid-write.
*/
func (a *App) Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A bare invocation reports what is running. It used to launch the window,
	// and the window is a separate program now, so the command line is a
	// command line.
	if len(args) == 0 {
		args = []string{"status"}
	}
	a.Root.SetArgs(args)
	a.Root.SetOut(stdout)
	a.Root.SetErr(stderr)

	err := a.Root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		if exit.Msg != "" {
			_, _ = fmt.Fprintln(stderr, exit.Msg)
		}
		return exit.Code
	}
	_, _ = fmt.Fprintf(stderr, "[ERROR] %s\n", err)
	var usage *UsageError
	if errors.As(err, &usage) {
		return ExitUsage
	}
	return ExitFailure
}

// attachToGame is the production attachment: find the game and open it twice.
func attachToGame() (*Game, error) {
	svc, err := service.Connect()
	if err != nil {
		return nil, err
	}
	mem := proc.New(svc.PID)
	return &Game{Svc: svc, Patcher: patch.NewPatcher(mem, svc.PID), PID: svc.PID}, nil
}

/*
game attaches, elevating first unless something already has.

`guard` is the build gate, which is a gate on *writing*: a read against a build
whose offsets moved finds no player, because the locator validates every match,
while a write against one puts numbers into the wrong fields of a live save.
*/
func (a *App) game(guard, force bool) (*Game, error) {
	if a.warm != nil {
		if guard {
			if err := a.warm.Svc.RequireCompatible(force); err != nil {
				return nil, err
			}
		}
		return a.warm, nil
	}
	if err := a.elevate(); err != nil {
		return nil, fmt.Errorf("could not become root: %w", err)
	}
	got, err := a.attach()
	if err != nil {
		return nil, err
	}
	if guard {
		if err := got.Svc.RequireCompatible(force); err != nil {
			return nil, err
		}
	}
	return got, nil
}

// out and errOut are where a command writes, which a test can capture.
func out(cmd *cobra.Command) io.Writer { return cmd.OutOrStdout() }

// printf writes one line of ordinary output.
func printf(cmd *cobra.Command, format string, args ...any) {
	_, _ = fmt.Fprintf(out(cmd), format+"\n", args...)
}

// printJSON writes a value as one line of JSON, which is the contract the
// window reads.
func printJSON(cmd *cobra.Command, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode the reply: %w", err)
	}
	_, _ = fmt.Fprintln(out(cmd), string(raw))
	return nil
}

// forceFlag is the gate override every writing command takes.
func forceFlag(cmd *cobra.Command, into *bool) {
	cmd.Flags().BoolVar(into, "force", false,
		"run even if the game build is not the known-good one")
}

// jsonFlag is the machine-readable switch.
func jsonFlag(cmd *cobra.Command, into *bool) {
	cmd.Flags().BoolVar(into, "json", false, "machine-readable output")
}
