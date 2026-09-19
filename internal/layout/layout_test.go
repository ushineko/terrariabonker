package layout_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Every offset is the Python's offset, and there are no others on either side.

The Python spreads them over five modules and spells several of them twice under
different names; this declares each one once. So the comparison is against the
union of the five, a name appearing in more than one module must agree with
itself, and a second Python name for a number Go already has is listed as an
alias and checked for agreeing in value rather than being declared again. Adding
a second Go spelling to satisfy the test would be the exact duplication this
package exists to end.

These numbers are build-specific and hand-derived, and they now exist in two
languages -- which is the exact failure the Python module was created to end,
after they were once spelled five times under four names. Nothing here checks a
number against what was typed beside it: the whole set is asked for and compared
by name, so an offset added on one side and missed on the other fails rather than
diverging quietly.
*/
func TestEveryOffsetMatchesThePython(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `
import json
from terrariabonker import inventory, layout, npcs, player, recipes
out = {}
for mod in (layout, player, inventory, npcs, recipes):
    for k, v in vars(mod).items():
        if not k.isupper() or not isinstance(v, int) or isinstance(v, bool):
            continue
        assert out.get(k, v) == v, f"{k} disagrees with itself across modules"
        out[k] = v
print(json.dumps(out))
`) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)

	var want map[string]int64
	require.NoError(t, json.Unmarshal(out, &want))
	require.NotEmpty(t, want)

	for name, value := range want {
		if target, isAlias := aliases[name]; isAlias {
			got, known := layout.Offsets[target]
			require.Truef(t, known, "%s is aliased to %s, which does not exist", name, target)
			require.Equalf(t, value, got,
				"%s and %s are the same number in the Python and differ here", name, target)
			continue
		}
		got, known := layout.Offsets[name]
		require.Truef(t, known, "%s is an offset the Python has and this does not", name)
		require.Equalf(t, value, got, "%s is a different number here", name)
	}
	for name := range layout.Offsets {
		_, known := want[name]
		require.Truef(t, known, "%s is an offset this has and the Python does not", name)
	}
}

/*
aliases are the numbers the Python spells twice, and the one Go name for each.

Every one of these is a module importing a constant from another and re-exporting
it under a shorter name, so the two spellings cannot disagree there. They could
disagree here, which is what the test above checks: the alias is followed to the
single Go declaration and the value compared.

A new entry belongs here only when the Python really does have two names for one
number. A Go constant added to satisfy one would be the duplication this package
was created to end.
*/
var aliases = map[string]string{
	"ARR_LEN":  "ARR_LEN_OFF",
	"ARR_DATA": "ARR_DATA_OFF",
}
