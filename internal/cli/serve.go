package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

/*
The long-lived privileged worker the window talks to.

The point is cost. Locating the player is nearly all of a read -- a full memory
scan -- so a one-shot run pays seconds where a warm request pays milliseconds,
and that is what makes the window's inventory sync affordable at one hertz.

The protocol is one JSON request per line in and one reply per line out, and the
stream is the protocol: every reply is flushed, and the worker exits on stdin
EOF so it cannot outlive the window that started it.
*/

/*
ServeOps are the subcommands the worker will run.

Everything else is refused, and deliberately: the window itself, the blocking
freeze loop, the raw memory pokes, and the slow unprivileged disk work the
window already does without sudo. A worker that ran anything would be a root
shell with a JSON front door.
*/
var ServeOps = map[string]bool{
	"status": true, "version": true, "inventory": true, "inv": true,
	"set-hp": true, "set-max-hp": true, "set-mana": true, "set-max-mana": true,
	"set-stack": true, "set-item": true, "give": true, "patch": true,
	"restore": true, "fast-mining": true, "long-reach": true, "compendium": true,
	"spawn-npc": true, "build-check": true, "accept-build": true,
	"vein": true, "extract": true, "extract-tick": true, "extract-stop": true,
	"potions": true, "fishing": true, "fishing-buffs": true,
	"catch": true, "catch-tick": true, "catch-stop": true,
	"projectile-tick": true, "projectile-stop": true, "projectile-of": true,
	"sell": true, "sell-tick": true, "sell-list": true,
}

// request is one line in.
type request struct {
	ID   json.RawMessage `json:"id"`
	Argv []string        `json:"argv"`
}

// reply is one line out.
type reply struct {
	ID  json.RawMessage `json:"id"`
	OK  bool            `json:"ok"`
	Out string          `json:"out"`
}

func (a *App) serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "long-lived JSON worker for the GUI (stdin/stdout protocol)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A no-op when the window already started this under sudo.
			if err := a.elevate(); err != nil {
				return fmt.Errorf("could not become root: %w", err)
			}
			return a.Serve(ctxOf(cmd), cmd.InOrStdin(), out(cmd))
		},
	}
}

/*
Serve runs the worker until its input ends.

One malformed request is answered and forgotten rather than ending the worker:
the window is on the other end of this and losing it means the panel goes dead
until somebody restarts it.
*/
func (a *App) Serve(ctx context.Context, in io.Reader, w io.Writer) error {
	lines := bufio.NewScanner(in)
	// A compendium reply is megabytes, and the default line limit is 64KB.
	lines.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for lines.Scan() {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // a cancelled context is not a failure: report what was done
		}
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			a.answer(w, nil, false, fmt.Sprintf("[ERROR] malformed request: %s", err))
			continue
		}
		if len(req.Argv) == 0 {
			a.answer(w, req.ID, false, "[ERROR] (empty) is not served here")
			continue
		}
		if !ServeOps[req.Argv[0]] {
			a.answer(w, req.ID, false,
				fmt.Sprintf("[ERROR] %s is not served here", req.Argv[0]))
			continue
		}
		/*
			Drop the warm attachment when the game went away, so the next
			request reconnects to whatever is running now: a restart is a new
			pid, and the old addresses belong to a process that has exited.
		*/
		if a.warm != nil && !a.alive(a.warm.PID) {
			a.warm = nil
		}
		if a.warm == nil {
			got, err := a.attach()
			if err != nil {
				a.answer(w, req.ID, false, fmt.Sprintf("[ERROR] %s", err))
				continue
			}
			a.warm = got
		}
		ok, out := a.once(ctx, req.Argv)
		a.answer(w, req.ID, ok, out)
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("read the request stream: %w", err)
	}
	return nil
}

/*
once runs one already-allowed argv against the warm attachment, capturing what
it wrote.

Both channels are captured into one string, which is what the window's
subprocess would have seen: it reads a merged stream, so a failure printed to
stderr has to arrive in the same place as the answer or it is lost.
*/
func (a *App) once(ctx context.Context, argv []string) (bool, string) {
	var buf bytes.Buffer
	sub := NewApp(Options{Attach: a.attach, Elevate: a.elevate, Alive: a.alive})
	sub.warm = a.warm
	code := sub.Execute(ctx, argv, &buf, &buf)
	return code == ExitOK, buf.String()
}

// answer writes one reply and flushes it, because the stream is the protocol.
func (a *App) answer(w io.Writer, id json.RawMessage, ok bool, out string) {
	raw, err := json.Marshal(reply{ID: id, OK: ok, Out: out})
	if err != nil {
		return
	}
	_, _ = w.Write(append(raw, '\n'))
	if f, canFlush := w.(interface{ Sync() error }); canFlush {
		_ = f.Sync()
	}
}

// alive reports whether a pid is still there, which is how a game restart is
// noticed.
func alive(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}
