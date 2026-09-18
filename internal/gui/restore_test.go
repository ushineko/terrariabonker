package gui

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// inWorld is a status with a located player, a pid and a world.
func inWorld(pid int, world string) *client.Status {
	name := "Nakama"
	return &client.Status{PID: pid, Name: &name, World: json.RawMessage(world)}
}

// poll feeds one status reading to the two things that hang off the status poll.
func poll(u *ui, st *client.Status) {
	u.noteWorld(st)
	u.maybeRestore(st)
}

/*
Nothing is written to a world that has not settled.

A world load is the most turbulent moment in the game's lifetime: the world, the
player and the arrays the trainer writes into are all being rebuilt, and the
player becomes locatable partway through. The game crashed twice during
structural operations while the trainer was writing, so this is caution rather
than a fix for a known cause. It costs one poll.
*/
func TestAWorldMustHoldStillBeforeAnythingIsWrittenToIt(t *testing.T) {
	u := testUI(t)
	st := inWorld(4242, `"Terraria"`)

	poll(u, st)
	require.False(t, u.worldSettled(), "one reading is not a settled world")
	require.Zero(t, u.rs.pid, "and nothing was restored into it")

	poll(u, st)
	require.True(t, u.worldSettled())
	require.Equal(t, 4242, u.rs.pid, "a settled world is restored into once")
}

/*
A new world is as good a reason to restore as a new process.

Keying on the pid alone was the bug: the game keeps its process across a world
switch while rebuilding the player from the save, so the edits were gone and
nothing put them back until the trainer was restarted.
*/
func TestAWorldSwitchRestoresEvenThoughThePidIsTheSame(t *testing.T) {
	u := testUI(t)
	for range 2 {
		poll(u, inWorld(4242, `"Underworld"`))
	}
	require.Equal(t, `"Underworld"`, u.rs.world, "the world it was put back into")

	// A restore starts its pass count again, so a sentinel that survives says
	// no second restore ran. The same game polled again is not a fresh one.
	u.rs.attempts = 99
	poll(u, inWorld(4242, `"Underworld"`))
	require.Equal(t, 99, u.rs.attempts)

	// Another world in the same process: put it back, once it has settled.
	poll(u, inWorld(4242, `"Sky"`))
	require.Equal(t, 99, u.rs.attempts, "a changed world has to settle first")
	poll(u, inWorld(4242, `"Sky"`))
	require.Equal(t, 1, u.rs.attempts, "a world switch is a fresh restore")
	require.Equal(t, `"Sky"`, u.rs.world)
}

// A new process is the other fresh game, and the commoner one: every patch
// lives in the process that was closed.
func TestANewProcessRestores(t *testing.T) {
	u := testUI(t)
	for range 2 {
		poll(u, inWorld(4242, `"Terraria"`))
	}
	u.rs.attempts = 99
	poll(u, inWorld(5151, `"Terraria"`))
	require.Equal(t, 1, u.rs.attempts)
	require.Equal(t, 5151, u.rs.pid)
}

/*
A world the status cannot identify is not a different world.

Unreadable comes back as null, and treating that as a change would re-apply the
whole profile several times a second for as long as it stayed unreadable.
*/
func TestAnUnreadableWorldIsNotAChangedWorld(t *testing.T) {
	u := testUI(t)
	for range 2 {
		poll(u, inWorld(4242, `"Terraria"`))
	}
	require.Equal(t, 1, u.rs.attempts)

	u.rs.attempts = 99
	for range 3 {
		poll(u, inWorld(4242, `null`))
	}
	require.Equal(t, 99, u.rs.attempts, "null is not the name of another world")
}

// A game with no player in it is not in-world yet, whatever else the status says.
func TestNothingIsRestoredBeforeThereIsAPlayer(t *testing.T) {
	u := testUI(t)
	for range 3 {
		poll(u, &client.Status{PID: 4242, World: json.RawMessage(`"Terraria"`)})
	}
	require.Zero(t, u.rs.attempts)
}

/*
A pass says how far it got, and a cheat still waiting is not a failure.

A cold restore takes about eighty seconds. The Qt panel went quiet after the
first pass, which reads as a hang, so every pass reports.
*/
func TestEachPassSaysHowFarItGot(t *testing.T) {
	require.Equal(t, "", restoreProgress(&client.Restore{}, 1),
		"a pass with nothing to report is not worth a line")
	require.Equal(t, "[auto-restore] 3 cheats applied",
		restoreProgress(&client.Restore{Cheats: []string{"a", "b", "c"}}, 1))

	line := restoreProgress(&client.Restore{
		Cheats: []string{"a"}, Pending: []string{"fast_place", "reach"}}, 4)
	require.Contains(t, line, "1 applied, 2 waiting on the game")
	require.Contains(t, line, "the first time you use that feature")
	require.Contains(t, line, "pass 4")
}

/*
What a finished restore could not do is reported by kind.

Cheats and item edits fail for unrelated reasons: a cheat's pattern does not
resolve on this build, while an item edit simply has nothing to apply to. An
item the player is not carrying is not a failure at all (spec 038).
*/
func TestTheSummarySeparatesTheThreeWaysARestoreFallsShort(t *testing.T) {
	u := testUI(t)
	u.iv.names = map[int]string{757: "Terra Blade"}

	require.Empty(t, u.restoreSummary(&client.Restore{Cheats: []string{"reach"}}),
		"a restore that finished has nothing to explain")

	lines := u.restoreSummary(&client.Restore{
		Pending: []string{"fast_place"},
		Skipped: []string{"cheat:mining", "something-else"},
		Absent:  []int{757},
	})
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "gave up on 1 cheat(s) after retrying: fast_place")
	require.Contains(t, lines[1], "1 cheat(s) refused: mining")
	require.NotContains(t, lines[1], "something-else", "only cheat: entries are cheats")
	require.Contains(t, lines[2], "waiting for an item you are not carrying: Terra Blade")
}

// The report distinguishes a pass that did something from one that did not, so
// a quiet game does not get a line of empty lists in the log.
func TestAPassWithNothingInItIsNotReported(t *testing.T) {
	require.False(t, (&client.Restore{}).Did())
	require.False(t, (&client.Restore{Absent: []int{757}}).Did(),
		"an item you are not carrying is not something the pass did")
	require.True(t, (&client.Restore{Cheats: []string{"reach"}}).Did())
	require.True(t, (&client.Restore{Pending: []string{"fast_place"}}).Did())
}
