package patch_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// hexOf is bytes as the Python prints them, so the two can be compared as one
// string rather than as two lists that differ in how they were built.
func hexOf(b []byte) string { return hex.EncodeToString(b) }

// quote is a Go value as a Python literal, via JSON, which the two agree on.
func quote(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// pyStr is one string as a Python literal.
func pyStr(s string) string { return quote(s) }

// pyBuild is a build key as the Python takes it: None when there is none.
func pyBuild(build string) string {
	if build == "" {
		return "None"
	}
	return fmt.Sprintf("%q", build)
}

// pyBool is a bool as Python spells it.
func pyBool(v bool) string {
	if v {
		return "True"
	}
	return "False"
}
