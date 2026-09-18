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
from terrariabonker import layout
print(json.dumps({k: v for k, v in vars(layout).items()
                  if k.isupper() and isinstance(v, int)}))
`) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)

	var want map[string]uint32
	require.NoError(t, json.Unmarshal(out, &want))
	require.NotEmpty(t, want)

	for name, value := range want {
		got, known := layout.Offsets[name]
		require.Truef(t, known, "%s is an offset the Python has and this does not", name)
		require.Equalf(t, value, got, "%s is a different number here", name)
	}
	for name := range layout.Offsets {
		_, known := want[name]
		require.Truef(t, known, "%s is an offset this has and the Python does not", name)
	}
}
