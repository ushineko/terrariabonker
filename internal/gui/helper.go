package gui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

/*
helper is the window's long-lived privileged worker: the other end of the CLI's
`serve`.

Locating the player is ~99% of a read's cost -- a full memory scan -- and a
one-shot CLI run pays it every time, so an `inventory --all --json` round trip
costs ~2.7 s. Keeping one `terrariabonker serve` process under sudo and sending
it JSON lines brings that to ~2.5 ms, which is what makes a 1 Hz inventory sync
affordable.

The protocol is one JSON request per line on stdin -- {"id": N, "argv": [...]} --
and one reply per line on stdout -- {"id": N, "ok": bool, "out": str}. The worker
exits on stdin EOF, so it cannot outlive the window that started it.

If the worker cannot start, dies, or refuses a command, callers fall back to
spawning the CLI per action. Available() says which.
*/
type helper struct {
	prog string
	argv []string
	note func(string)

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	nextID  int
	pending map[int]chan reply
	dead    bool
}

// reply is one decoded response line.
type reply struct {
	OK  bool   `json:"ok"`
	Out string `json:"out"`
	ID  int    `json:"id"`
}

// request is one line written to the worker.
type request struct {
	ID   int      `json:"id"`
	Argv []string `json:"argv"`
}

// helperTimeout bounds a single request. The worker is warm, so a request that
// takes this long is a worker that is not coming back rather than one that is
// working hard; the caller falls back to a one-shot run rather than hanging the
// section that asked.
const helperTimeout = 30 * time.Second

// newHelper starts one worker. A nil error does not promise the worker is
// healthy -- sudo may still reject us, and the process may exit a moment from
// now -- only that it was spawned; the first request finds out.
func newHelper(prog string, argv []string, note func(string)) (*helper, error) {
	h := &helper{prog: prog, argv: argv, note: note, pending: map[int]chan reply{}}
	// context.Background deliberately: this process outlives every request, and
	// is stopped by closing its stdin (which is what it waits on) rather than by
	// cancellation.
	cmd := exec.CommandContext(context.Background(), prog, argv...) //nolint:gosec // argv is built by the client package, never from input
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("worker stdout: %w", err)
	}
	// sudo's complaints go to stderr; keep them out of the JSON stream and
	// report them, because "sudo needs a password" is the one failure a person
	// can act on and the window has nowhere to prompt.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("worker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the worker: %w", err)
	}
	h.cmd, h.stdin = cmd, stdin
	go h.read(stdout)
	go h.drainErr(stderr)
	return h, nil
}

// read decodes reply lines and hands each to whoever is waiting for its id.
func (h *helper) read(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // an inventory reply is large
	for sc.Scan() {
		var r reply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // not a reply line; the worker only ever writes these
		}
		h.mu.Lock()
		ch, ok := h.pending[r.ID]
		delete(h.pending, r.ID)
		h.mu.Unlock()
		if ok {
			ch <- r
		}
	}
	// The stream ended: the worker is gone. Fail everything still waiting so no
	// caller sits on a channel that will never be written.
	h.mu.Lock()
	h.dead = true
	for id, ch := range h.pending {
		close(ch)
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

// drainErr reports the worker's stderr, a line at a time.
func (h *helper) drainErr(stderr io.Reader) {
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		if line := sc.Text(); line != "" && h.note != nil {
			h.note("worker: " + line)
		}
	}
}

// Available reports whether the worker is still there to be asked.
func (h *helper) Available() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.dead && h.stdin != nil
}

// Do sends one request and waits for its reply. Safe to call from a goroutine;
// callers are operations running off the UI thread.
func (h *helper) Do(argv []string) (string, error) {
	if !h.Available() {
		return "", errors.New("the privileged worker is not running")
	}
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	ch := make(chan reply, 1)
	h.pending[id] = ch
	line, err := json.Marshal(request{ID: id, Argv: argv})
	if err != nil {
		delete(h.pending, id)
		h.mu.Unlock()
		return "", fmt.Errorf("encoding the request: %w", err)
	}
	_, err = h.stdin.Write(append(line, '\n'))
	h.mu.Unlock()
	if err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return "", fmt.Errorf("writing to the worker: %w", err)
	}

	select {
	case r, ok := <-ch:
		if !ok {
			return "", errors.New("the privileged worker exited")
		}
		if !r.OK {
			return r.Out, fmt.Errorf("%s", r.Out)
		}
		return r.Out, nil
	case <-time.After(helperTimeout):
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return "", fmt.Errorf("the privileged worker did not answer in %s", helperTimeout)
	}
}

// Close shuts the worker down by closing its stdin, which is what it waits on.
func (h *helper) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	stdin, cmd := h.stdin, h.cmd
	h.stdin, h.dead = nil, true
	h.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		// It exits on EOF; wait briefly, then stop waiting. A worker that will
		// not go is the operating system's problem, not a reason to hang here.
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
}
