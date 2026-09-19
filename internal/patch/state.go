package patch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

/*
The patch state is what this program remembers about a game it has already
patched: where each anchor resolved, which cheats are on, where each stub was
put and what value it was given.

It exists because the trainer is not one long-lived process. The window shells
out for every operation, so "is reach on?" is answered by a process that has
never enabled anything, and the addresses a scan cost eight seconds to find have
to survive between runs. It is keyed by pid: a different game is a different
state, and the file is ignored rather than trusted.
*/

// StatePath is where the record lives.
func StatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "terrariabonker", "patches.json")
}

// Site is one place an injection was installed: the hooked address and the cave
// holding its stub.
type Site struct {
	Inject uint32 `json:"inject"`
	Cave   uint32 `json:"cave"`
}

// Installed is one injection as it currently stands in the game.
type Installed struct {
	Sites   []Site `json:"sites"`
	StubLen int    `json:"stub_len"`
}

/*
State is the record, as it is written to disk.

Sites hold every address an anchor matched, not just the first: mono can JIT one
method into more than one arena and a cheat patches all of them.
*/
type State struct {
	PID     int                  `json:"pid"`
	Sites   map[string][]uint32  `json:"sites"`
	Enabled []string             `json:"enabled"`
	Inj     map[string]Installed `json:"inj"`
	Values  map[string]float64   `json:"values"`
	Arena   uint32               `json:"arena"`
}

/*
LoadState reads the record for a pid, or an empty one.

A record naming a different process is discarded rather than adapted: those
addresses belong to a game that has exited, and writing to them would land in
whatever occupies that memory now. Anything unreadable or malformed is treated
the same way -- there is nothing here that is worth failing an operation over,
because a rescan reproduces all of it.
*/
func LoadState(pid int) State {
	empty := State{
		PID: pid, Sites: map[string][]uint32{}, Inj: map[string]Installed{},
		Values: map[string]float64{}, Enabled: []string{},
	}
	raw, err := os.ReadFile(StatePath())
	if err != nil {
		return empty
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return empty
	}
	if s.PID != pid {
		return empty
	}
	return normalise(s, pid)
}

/*
normalise fills in what an older record may not have had.

The record has been written by more than one version of this program, and a
missing map is not a reason to throw away a live game's patch state: an upgrade
in place has to keep working, because the alternative is a process full of
installed stubs that nothing remembers how to remove.
*/
func normalise(s State, pid int) State {
	s.PID = pid
	if s.Sites == nil {
		s.Sites = map[string][]uint32{}
	}
	if s.Inj == nil {
		s.Inj = map[string]Installed{}
	}
	if s.Values == nil {
		s.Values = map[string]float64{}
	}
	if s.Enabled == nil {
		s.Enabled = []string{}
	}
	return s
}

/*
Save writes the record atomically.

Through a temporary file and a rename, because the window reads this while a
patch is being applied: a half-written file is a status that reports nothing
installed while stubs are live in the game, and acting on that would install them
twice.
*/
func (s State) Save() error {
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("make the state directory: %w", err)
	}
	sort.Strings(s.Enabled)
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode the state: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil { //nolint:gosec // read by the same user's window
		return fmt.Errorf("write the state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace the state: %w", err)
	}
	return nil
}

/*
WithLock runs a mutating operation with the state file held exclusively, and
re-reads it under the lock before handing it over.

Two invocations can be in flight at once -- the window toggling several cheats
applies them one process at a time -- and without this the second reads the state
before the first has written it, then saves its own version over the top. The
cheat is installed in the game and the record says it is not, so nothing can turn
it off again.
*/
func WithLock(pid int, do func(*State) error) error {
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("make the state directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644) //nolint:gosec // a lock file beside the state
	if err != nil {
		return fmt.Errorf("open the state lock: %w", err)
	}
	defer func() { _ = lock.Close() }()

	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("take the state lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	state := LoadState(pid)
	if err := do(&state); err != nil {
		return err
	}
	return state.Save()
}

// IsEnabled reports whether a cheat is recorded as on.
func (s State) IsEnabled(name string) bool {
	for _, n := range s.Enabled {
		if n == name {
			return true
		}
	}
	return false
}

// SetEnabled records a cheat as on or off.
func (s *State) SetEnabled(name string, on bool) {
	out := s.Enabled[:0]
	for _, n := range s.Enabled {
		if n != name {
			out = append(out, n)
		}
	}
	s.Enabled = out
	if on {
		s.Enabled = append(s.Enabled, name)
	}
	sort.Strings(s.Enabled)
}

/*
InstalledCaves is every stub currently installed, as address and length.

A cave is only free when nothing of this program's is in it. The cave search
looks for cold bytes, and a disabled stub's cave is scrubbed to 0xCC -- which is
exactly what it hunts for -- so without this an enable and disable cycle turns a
used cave into bait.
*/
func (s State) InstalledCaves() [][2]uint32 {
	var out [][2]uint32
	for _, rec := range s.Inj {
		if rec.StubLen == 0 {
			continue
		}
		for _, site := range rec.Sites {
			if site.Cave != 0 {
				out = append(out, [2]uint32{site.Cave, uint32(rec.StubLen)}) //nolint:gosec // a stub length
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// ErrNoState is returned when there is nothing recorded for this process.
var ErrNoState = errors.New("no patch state for this process")
