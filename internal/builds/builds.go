/*
Package builds is the game builds this machine has decided about.

Separate from the verified sets in the patch package on purpose. Those are the
project's claim: somebody derived the AOBs against that exact build and
confirmed the cheats in play. What lives here is weaker and local -- "on this
machine, on this build, the patterns still matched, and I chose to carry on" --
so it is recorded apart and presented differently.

Same shape as the profile: a small JSON file under the config directory, written
atomically under a lock, and disposable. Losing it only brings the dialog back.

Ported from terrariabonker/builds.py (spec 051, step 5).
*/
package builds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// The two decisions a person can make about a build.
const (
	// Accepted: every cheat resolved and the user accepted the build.
	Accepted = "accepted"
	// Degraded: some did not, and the user chose to carry on anyway.
	Degraded = "degraded"
)

// Path is where the decisions live.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "terrariabonker", "accepted-builds.json")
}

/*
Decision is what was decided about one build.

Runtime is the .NET runtime that was executing the game, e.g.
"wine-mono-11.2.0". Recorded because the patches match code that runtime's JIT
emitted: a decision made under one runtime says nothing about another, even for
the same game build. It is kept beside the key rather than folded into it, so a
decision made before this was tracked stays valid and simply reports no runtime
-- which is the truth about it.

Failed is the cheats that did not resolve, kept so the panel can disable exactly
those on a later launch without re-probing.
*/
type Decision struct {
	Decision string   `json:"decision"`
	Failed   []string `json:"failed"`
	Runtime  string   `json:"runtime,omitempty"`
}

// Load is every decision on this machine, or nothing when there is no file.
func Load() map[string]Decision {
	blob, err := os.ReadFile(Path()) //nolint:gosec // a path this package built
	if err != nil {
		return map[string]Decision{}
	}
	var out map[string]Decision
	if err := json.Unmarshal(blob, &out); err != nil || out == nil {
		return map[string]Decision{}
	}
	return out
}

// Get is what this machine already decided about a build, if it has.
func Get(buildKey string) (Decision, bool) {
	got, known := Load()[buildKey]
	return got, known
}

// Remember records a decision so the dialog does not ask again for this build.
func Remember(buildKey, how string, failed []string, runtime string) error {
	return update(func(all map[string]Decision) bool {
		// Copied into a non-nil slice so it marshals as [] rather than null:
		// the Python always writes a list, and a decision that recorded no
		// failures is not a decision that recorded nothing.
		sorted := append([]string{}, failed...)
		sort.Strings(sorted)
		all[buildKey] = Decision{Decision: how, Failed: sorted, Runtime: runtime}
		return true
	})
}

// Forget drops a decision, so the panel asks about that build again.
func Forget(buildKey string) error {
	return update(func(all map[string]Decision) bool {
		if _, known := all[buildKey]; !known {
			return false
		}
		delete(all, buildKey)
		return true
	})
}

// FailedCheats is the cheats recorded as not resolving on a build.
func FailedCheats(buildKey string) map[string]bool {
	out := map[string]bool{}
	got, known := Get(buildKey)
	if !known {
		return out
	}
	for _, name := range got.Failed {
		out[name] = true
	}
	return out
}

/*
update runs a change with the file held exclusively, re-reading it under the
lock.

The same reason the profile has one: several invocations can be in flight at
once, and without it the second writes a file that does not know about the
first. A change that says nothing happened writes nothing, so forgetting a
build nobody decided about does not rewrite the file.
*/
func update(change func(map[string]Decision) bool) error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("make the config directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644) //nolint:gosec // a lock beside the file
	if err != nil {
		return fmt.Errorf("open the builds lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("take the builds lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	all := Load()
	if !change(all) {
		return nil
	}
	/*
		Indented and key-sorted, which is how the Python writes it: the file is
		shared between the two implementations while both exist, and one of them
		rewriting it wholesale on every save would make every decision look like
		a change.
	*/
	raw, err := json.MarshalIndent(all, "", " ")
	if err != nil {
		return fmt.Errorf("encode the builds: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil { //nolint:gosec // the user's own settings
		return fmt.Errorf("write the builds: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("put the builds in place: %w", err)
	}
	return nil
}
