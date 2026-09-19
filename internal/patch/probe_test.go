package patch_test

import (
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
