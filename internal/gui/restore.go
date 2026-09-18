package gui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
Auto-restore (spec 049).

A patch lives in the running process, so a game restart clears every one of them
while the profile still says they should be on. So does loading another world:
the game keeps its process and rebuilds the player from the save, which was the
bug -- keying on the pid alone meant the edits were gone and nothing put them
back until the trainer was restarted.

It is slow. A cold pass costs around fourteen seconds to re-resolve the anchors
and the whole thing takes about eighty, so it reports progress on every pass
rather than going quiet after the first, which reads as a hang.
*/
type restoreState struct {
	// seen is the world the game last reported and settle is how many polls it
	// has held still. Nothing is written to a world that has not settled: a
	// world load is the most turbulent moment in the game's lifetime, and the
	// player becomes locatable partway through it.
	seen   string
	settle int
	// pid and world are what the profile was last put back into.
	pid   int
	world string
	// attempts, said and left are one restore's progress: which pass this is,
	// the last line reported, and what was still outstanding after the previous
	// pass -- two passes with the same leftovers means no progress.
	attempts int
	said     string
	left     string
	inflight bool
}

/*
How long to keep trying, and how long a world must hold still first.

Retries exist because some cheats hook methods the game only JIT-compiles when
the feature is first used: fast placement compiles when the player first places
a block, so it genuinely cannot be applied before then.
*/
const (
	restoreRetries    = 8
	restoreRetryAfter = 2 * time.Second
	worldSettlePolls  = 2
)

// noteWorld tracks which world the game reports and how long it has held still.
func (u *ui) noteWorld(st *client.Status) {
	world := string(st.World)
	if world != u.rs.seen {
		u.rs.seen, u.rs.settle = world, 0
	}
	u.rs.settle++
}

// worldSettled reports whether the world has held still long enough to write to.
func (u *ui) worldSettled() bool { return u.rs.settle >= worldSettlePolls }

/*
maybeRestore puts the profile back when a fresh in-world game turns up.

"Fresh" is a new pid or a new world. A world the status cannot identify must not
read as a different world on every poll, or the profile would be re-applied
several times a second.
*/
func (u *ui) maybeRestore(st *client.Status) {
	if st.PID == 0 || st.Name == nil || *st.Name == "" {
		return // no located player: the game is not in-world yet
	}
	if !u.worldSettled() || u.rs.inflight {
		return
	}
	world := string(st.World)
	known := world != "" && world != "null"
	fresh := st.PID != u.rs.pid || (known && world != u.rs.world)
	if !fresh {
		return
	}
	u.rs.pid, u.rs.world = st.PID, world
	u.rs.attempts, u.rs.left, u.rs.said = 0, "", ""
	u.restoreProfile()
}

// restoreProfile runs one pass and decides whether another is worth making.
func (u *ui) restoreProfile() {
	u.rs.attempts++
	u.rs.inflight = true
	u.sh.Load("Putting the saved patches back...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.RestoreArgv())
		rep, ok := client.ParseRestore(out)
		fyne.Do(func() {
			u.rs.inflight = false
			if !ok {
				u.restoreFailed(out, err)
				return
			}
			u.reportRestore(rep)
		})
		return nil
	})
}

/*
restoreFailed decides what an unreadable pass means.

Worth retrying rather than giving up. The refusal that actually happens is a
start-up race -- the version string is scanned out of live memory and for a
moment after launch the game's own has not been allocated -- so giving up on the
first error killed auto-restore for a whole session over a condition that clears
itself within seconds.
*/
func (u *ui) restoreFailed(out string, err error) {
	if u.rs.attempts < restoreRetries {
		u.retryRestore()
		return
	}
	if line := firstLine(detail(out, err)); line != "" {
		u.note("[auto-restore FAILED] " + line)
	}
}

// reportRestore says how far a pass got, and schedules another while another
// would achieve something.
func (u *ui) reportRestore(rep *client.Restore) {
	if u.rs.attempts == 1 && rep.Did() {
		u.note(fmt.Sprintf("[auto-restore] cheats=%v items=%v pending=%v skipped=%v",
			rep.Cheats, rep.Items, rep.Pending, rep.Skipped))
	}
	// Something on every pass, not only the first: a cold restore takes about
	// eighty seconds and going quiet after pass one reads as a hang.
	if line := restoreProgress(rep, u.rs.attempts); line != "" && line != u.rs.said {
		u.rs.said = line
		u.note(line)
	}
	u.loadPatches()

	left := strings.Join(append(sorted(rep.Pending), sorted(rep.Skipped)...), ",")
	progressed := left != u.rs.left
	u.rs.left = left
	switch {
	case left == "":
		return
	case progressed && u.rs.attempts < restoreRetries:
		u.retryRestore()
	case !progressed && u.rs.attempts > 1:
		// Retrying only helps while it is still getting somewhere. A cheat
		// whose method has not compiled yet resolves on a later pass; one that
		// cannot resolve on this build never will, and the reason for that is
		// on the Patches section rather than in a line repeated eight times.
		for _, line := range u.restoreSummary(rep) {
			u.note(line)
		}
	}
}

// retryRestore comes back in a couple of seconds. Only with a CLI to run: with
// nothing to call there is nothing a later pass could do differently.
func (u *ui) retryRestore() {
	if u.cli == "" {
		return
	}
	time.AfterFunc(restoreRetryAfter, func() { fyne.Do(func() { u.restoreProfile() }) })
}

/*
restoreProgress is one line saying how far a pass got, or "" when there is
nothing to say.

A cheat still waiting on the game is not a failure and must not read as one: it
applies the first time the player uses that feature.
*/
func restoreProgress(rep *client.Restore, attempt int) string {
	done, pending := len(rep.Cheats), len(rep.Pending)
	if pending == 0 {
		if done == 0 {
			return ""
		}
		return fmt.Sprintf("[auto-restore] %d cheats applied", done)
	}
	return fmt.Sprintf("[auto-restore] %d applied, %d waiting on the game "+
		"(they apply the first time you use that feature) -- pass %d", done, pending, attempt)
}

/*
restoreSummary is what a finished restore could not do, in plain words.

Cheats and item edits fail for unrelated reasons and must not be reported as one
lump: a cheat is missing because its pattern does not resolve on this build,
while an item edit simply has nothing to apply to. An item the player is not
carrying is not a failure at all (spec 038).
*/
func (u *ui) restoreSummary(rep *client.Restore) []string {
	var out []string
	if len(rep.Pending) > 0 {
		out = append(out, fmt.Sprintf("[auto-restore] gave up on %d cheat(s) after "+
			"retrying: %s -- the Patches section says why",
			len(rep.Pending), strings.Join(sorted(rep.Pending), ", ")))
	}
	var refused []string
	for _, s := range rep.Skipped {
		if name, cut := strings.CutPrefix(s, "cheat:"); cut {
			refused = append(refused, name)
		}
	}
	if len(refused) > 0 {
		out = append(out, fmt.Sprintf("[auto-restore] %d cheat(s) refused: %s",
			len(refused), strings.Join(sorted(refused), ", ")))
	}
	if len(rep.Absent) > 0 {
		names := make([]string, 0, len(rep.Absent))
		for _, t := range rep.Absent {
			names = append(names, u.itemName(t))
		}
		out = append(out, fmt.Sprintf("[auto-restore] %d saved item edit(s) waiting for "+
			"an item you are not carrying: %s", len(rep.Absent), strings.Join(sorted(names), ", ")))
	}
	return out
}

// sorted is a copy in order, so the same report reads the same way twice.
func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
