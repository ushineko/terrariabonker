package gui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/fynedesygn/fynetest"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// probe is a build report with the given cheats, the named ones dead.
func probe(recognised bool, cheats []string, dead ...string) *client.BuildCheck {
	r := &client.BuildCheck{
		Build: "1.4.5.9+12345", Message: "Terraria 1.4.5.9 differs from 1.4.5.8",
		Recognised: recognised, Cheats: map[string]client.CheatProbe{},
	}
	down := map[string]bool{}
	for _, n := range dead {
		down[n] = true
	}
	for _, n := range cheats {
		r.Cheats[n] = client.CheatProbe{Resolved: !down[n], Sites: 1}
		if down[n] {
			r.Cheats[n] = client.CheatProbe{Reason: "no match"}
			r.Failed = append(r.Failed, n)
		}
	}
	return r
}

/*
The gate waits for a player to be in-world.

Several cheats hook methods mono compiles lazily, so a probe at the main menu
reports them as unmatched. A dialog saying a cheat is dead when it is merely not
compiled yet is worse than no dialog at all.
*/
func TestTheGateWaitsForAPlayerAndAsksAboutEachBuildOnce(t *testing.T) {
	u := testUI(t)
	name := "Nakama"

	u.maybeGateBuild(&client.Status{Version: "1.4.5.9", Build: "1.4.5.9+1", BuildID: "1"})
	require.Empty(t, u.gate.asked, "no player yet: too early to judge")

	u.maybeGateBuild(&client.Status{Name: &name})
	require.Empty(t, u.gate.asked, "no build to key on")

	// A probe that cannot run is not consent. There is no CLI here, so the
	// check fails, and the build must be left unmarked so the next poll asks
	// again rather than carrying on as though it had been approved.
	st := &client.Status{Name: &name, Version: "1.4.5.9", Build: "1.4.5.9+1", BuildID: "1"}
	u.maybeGateBuild(st)
	require.NotContains(t, u.gate.asked, "1.4.5.9+1")
	require.False(t, u.gate.open)

	// A build that has been asked about is not asked about again, which is what
	// keeps a two-second poll from putting the question up every other second.
	u.gate.asked["1.4.5.9+1"] = true
	u.maybeGateBuild(st)
	require.Len(t, u.gate.asked, 1)
}

// A build key is version plus Steam buildid, because that pair is what a byte
// pattern is pinned to. Without a buildid the version is all there is.
func TestABuildIsKeyedByVersionAndBuildID(t *testing.T) {
	require.Equal(t, "1.4.5.8+249", (&client.Status{
		Version: "1.4.5.8", BuildID: "249", Build: "1.4.5.8+249"}).BuildKey())
	require.Equal(t, "1.4.5.8", (&client.Status{Version: "1.4.5.8"}).BuildKey())
	require.Equal(t, "", (*client.Status)(nil).BuildKey())
}

/*
Everything matching and some of it matching are different answers.

The button has to say which one is being agreed to: "OK" on both would make two
outcomes that mean very different things look like one.
*/
func TestTheGateAsksTheQuestionTheReportEarned(t *testing.T) {
	all := probe(false, []string{"reach", "mining", "pickup"})
	label, decision := gateChoice(all)
	require.Equal(t, "Accept this build", label)
	require.Equal(t, client.DecisionAccepted, decision)

	some := probe(false, []string{"reach", "mining", "pickup"}, "mining", "pickup")
	label, decision = gateChoice(some)
	require.Equal(t, "Continue without 2", label)
	require.Equal(t, client.DecisionDegraded, decision)

	texts := strings.Join(fynetest.Texts(gateBody(some)), " | ")
	require.Contains(t, texts, "2 of 3 cheats no longer match on this build:")
	require.Contains(t, texts, "mining")
	require.Contains(t, texts, "no match")
	require.Contains(t, texts, "1.4.5.9+12345", "the running build is named")

	texts = strings.Join(fynetest.Texts(gateBody(all)), " | ")
	require.Contains(t, texts, "All 3 cheats still match")
	require.NotContains(t, texts, "no longer match")
}

/*
A decision this machine already made is honoured without asking again.

A cheat recorded as dead stays off even when a later probe resolves it: the user
chose to run without it, and a window that quietly switched it back on would be
overriding that choice.
*/
func TestARememberedDecisionIsAppliedWithoutAsking(t *testing.T) {
	u := testUI(t)

	r := probe(true, []string{"reach", "mining"}, "mining")
	r.Decision = client.DecisionDegraded
	r.DecidedFailed = []string{"mining"}
	u.applyBuildDecision(r)
	require.True(t, u.gated("mining"))
	require.False(t, u.gated("reach"))
	require.False(t, u.gate.open, "a remembered decision asks nothing")

	ok := probe(true, []string{"reach", "mining"})
	ok.Decision = client.DecisionAccepted
	u.applyBuildDecision(ok)
	require.False(t, u.gate.open)
}

// An unrecognised build is a question, and the dialog stays up until it is
// answered: closing it by the window button is not consent.
func TestAnUnrecognisedBuildOpensTheQuestion(t *testing.T) {
	u := testUI(t)
	u.applyBuildDecision(probe(false, []string{"reach"}, "reach"))
	require.True(t, u.gate.open)
}

// The dialog grows with the list of dead cheats, and stops growing before it
// runs off the screen.
func TestTheGateDialogGrowsWithWhatItHasToList(t *testing.T) {
	small := gateHeight(probe(false, []string{"a"}, "a"))
	big := gateHeight(probe(false,
		[]string{"a", "b", "c", "d", "e", "f"}, "a", "b", "c", "d", "e", "f"))
	require.Greater(t, big, small)
	require.LessOrEqual(t, big, gateMaxHeight)
}

/*
A patch control says which of three things is wrong with it.

Off by the build gate, unavailable on this build, and resolving but unproven are
different facts, and only the first two stop the control working.
*/
func TestAPatchControlSaysWhyItCannotBeUsed(t *testing.T) {
	u := testUI(t)
	p := client.Patch{Name: "mining", Label: "Fast mining", Note: "Mine at any speed."}

	why, usable := u.patchStanding(p)
	require.True(t, usable, "nothing read yet is not a reason to refuse")
	require.Equal(t, p.Note, why)

	u.px.detail = map[string]client.PatchDetail{
		"mining": {Available: true, Verified: true},
	}
	why, usable = u.patchStanding(p)
	require.True(t, usable)
	require.Equal(t, p.Note, why)

	u.px.detail["mining"] = client.PatchDetail{Available: true}
	why, usable = u.patchStanding(p)
	require.True(t, usable, "unproven still works")
	require.Contains(t, why, "confirmed on a different one")

	u.px.detail["mining"] = client.PatchDetail{Reason: "pattern moved"}
	why, usable = u.patchStanding(p)
	require.False(t, usable)
	require.Contains(t, why, "pattern moved")

	u.setUnavailable([]string{"mining"})
	why, usable = u.patchStanding(p)
	require.False(t, usable)
	require.Contains(t, why, "you chose to carry on without it")
}

// The catalog's standing is a count, not a yes or no: every cheat working but
// unproven and four of twelve not resolving are not the same situation.
func TestTheCatalogsStandingCountsWhatIsWrong(t *testing.T) {
	u := testUI(t)
	require.Contains(t, fynetest.Texts(u.patchVerdict()), "not read")

	u.px.catalog = []client.Patch{{Name: "mining"}, {Name: "reach"}}
	u.px.verified = true
	u.px.detail = map[string]client.PatchDetail{
		"mining": {Available: true, Verified: true},
		"reach":  {Available: true, Verified: true},
	}
	require.Contains(t, fynetest.Texts(u.patchVerdict()), "verified")

	u.px.detail["reach"] = client.PatchDetail{Available: true}
	u.px.verified = false
	require.Contains(t, fynetest.Texts(u.patchVerdict()), "1 unproven here")

	u.px.detail["mining"] = client.PatchDetail{Reason: "pattern moved"}
	require.Contains(t, fynetest.Texts(u.patchVerdict()), "1 of 2 do not resolve here")
}
