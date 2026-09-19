package patch_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
What the window is told about a build.

The two answers differ only in ways that are easy to get backwards -- one is
about the build, the other about this session -- so both are compared against
the Python for every patch there is, with and without the patch applied. An
applied injection has overwritten its own anchor, which is exactly the case
where a fresh scan gives the wrong answer for the right reason.
*/

// A fresh game resolves the same everywhere, cheat for cheat.
func TestProbingAFreshGameMatchesThePython(t *testing.T) {
	home := atHome(t)
	var want map[string]any
	askPython(t, pyGame(home)+`
print(json.dumps(p.probe("1.4.5.7+24893155")))`, &want)

	mem := plantGame(t)
	got := newPatcher(t, mem).Probe("1.4.5.7+24893155")
	require.Equal(t, want, asJSON(t, got), "a different verdict on the build")
	require.NotEmpty(t, got, "nothing was probed at all")
	for name, r := range got {
		require.Truef(t, r.Resolved, "%s did not resolve on a game built to hold it", name)
	}
}

/*
And an applied patch reports as resolving, with the sites it is installed at.

This is the case the whole shape exists for: the injection's jump is written
over its own anchor, so a scan run now finds nothing -- and reporting that as a
failure would tell the user a working cheat is broken on their build.
*/
func TestProbingAnAppliedPatchMatchesThePython(t *testing.T) {
	for _, name := range everyPatch() {
		t.Run(name, func(t *testing.T) {
			home := atHome(t)
			var want map[string]any
			askPython(t, pyGame(home)+fmt.Sprintf(`
p.enable(%q)
print(json.dumps(p.probe("1.4.5.7+24893155")))`, name), &want)

			mem := plantGame(t)
			p := newPatcher(t, mem)
			require.NoError(t, p.Enable(name, nil))

			got := p.Probe("1.4.5.7+24893155")
			require.Equal(t, want, asJSON(t, got), "a different verdict on the build")
			require.True(t, got[name].Applied, "the applied patch was not reported as applied")
			require.True(t, got[name].Resolved,
				"the applied patch was reported as not resolving on the build it is running on")
			require.Positive(t, got[name].Sites, "an applied patch is installed nowhere")
		})
	}
}

// The window's own view agrees too, on a fresh game and on a patched one.
func TestTheDetailsMatchThePython(t *testing.T) {
	for _, name := range []string{"", "tool_reach", "mining", "ore_extract"} {
		label := "nothing applied"
		enable := ""
		if name != "" {
			label, enable = name+" applied", fmt.Sprintf("p.enable(%q)\n", name)
		}
		t.Run(label, func(t *testing.T) {
			home := atHome(t)
			var want map[string]any
			askPython(t, pyGame(home)+enable+`
print(json.dumps(p.details("1.4.5.7+24893155")))`, &want)

			mem := plantGame(t)
			p := newPatcher(t, mem)
			if name != "" {
				require.NoError(t, p.Enable(name, nil))
			}
			got := p.Details("1.4.5.7+24893155")
			require.Equal(t, want, asJSON(t, got), "a different view of the cheats")
			if name != "" {
				require.True(t, got[name].On, "the applied patch does not read as on")
				require.Positive(t, got[name].Sites, "an applied patch is installed nowhere")
			}
		})
	}
}

/*
A build nobody has verified is available and unverified, not unavailable.

The two are different claims -- "these patterns still match" and "somebody
checked this build" -- and collapsing them would have the window refuse to run
on a build where everything demonstrably works.
*/
func TestAnUnverifiedBuildStillResolves(t *testing.T) {
	atHome(t)
	mem := plantGame(t)
	got := newPatcher(t, mem).Details("9.9.9.9+1")
	for name, d := range got {
		require.Truef(t, d.Available, "%s stopped resolving on an unknown build", name)
		require.Falsef(t, d.Verified, "%s claims an unknown build was verified", name)
	}
}

// Every anchor key names an anchor that exists, whichever table declared the
// patch.
func TestEveryPatchResolvesThroughARealAnchor(t *testing.T) {
	for _, info := range patch.Catalog() {
		key := patch.AnchorKey(info.Name)
		require.NotEmptyf(t, key, "%s resolves through no anchor at all", info.Name)
		_, known := patch.Anchors[key]
		require.Truef(t, known, "%s resolves through %q, which is not an anchor",
			info.Name, key)
	}
}

/*
An anchor that matches nothing is reported as not resolving, with what was seen.

This is the answer the whole probe exists to give: after a game update some
patterns stop matching, and the window has to say which and let the user decide.
A fixture where everything resolves cannot tell a probe from a function that
returns true.
*/
func TestAnAnchorThatMatchesNothingMatchesThePython(t *testing.T) {
	const gone = "grabitems_call" // ore_extract's anchor, and nothing else's

	home := atHome(t)
	var want map[string]any
	askPython(t, pyGame(home)+fmt.Sprintf(`
mem.poke_bytes(SITES[%q], b"\x90" * len(P.ANCHORS[%q].pattern.raw))
print(json.dumps({"probe": p.probe("1.4.5.7+24893155"),
                  "details": p.details("1.4.5.7+24893155")}))`, gone, gone), &want)

	mem := plantGame(t)
	mem.PokeBytes(anchorSites[gone], make([]byte, patch.Anchors[gone].Pattern.Len()))
	p := newPatcher(t, mem)

	got := map[string]any{"probe": p.Probe("1.4.5.7+24893155"),
		"details": p.Details("1.4.5.7+24893155")}
	require.Equal(t, asPlainText(want), asJSON(t, got),
		"a different account of a missing anchor")

	require.False(t, p.Probe("1.4.5.7+24893155")["ore_extract"].Resolved,
		"a cheat whose anchor is gone was reported as resolving")
	require.NotEmpty(t, p.Details("1.4.5.7+24893155")["ore_extract"].Reason,
		"nothing was said about why it cannot be applied")
	require.True(t, p.Probe("1.4.5.7+24893155")["mining"].Resolved,
		"one anchor going took the others with it")
}

/*
asPlainText transliterates the Python's punctuation so the messages themselves
can be compared rather than skipped.

The two write the same sentence with different characters -- the Python uses an
em dash and single quotes, this package writes ASCII throughout -- and dropping
the reason from the comparison to get around that would leave the one field a
reader actually acts on unchecked.
*/
func asPlainText(v any) any {
	switch value := v.(type) {
	case string:
		return strings.NewReplacer("\u2014", "--", "'", `"`).Replace(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, inner := range value {
			out[k] = asPlainText(inner)
		}
		return out
	default:
		return v
	}
}
