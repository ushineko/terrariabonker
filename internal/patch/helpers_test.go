package patch_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

/*
realHome is the maintainer's home directory as it was before any test moved it.

Some tests here point Go at a scratch HOME so they never touch the real patch
state. The Python child must not inherit that: its interpreter works out where
the user's packages are at startup, from HOME, and a moved one makes numpy
unimportable -- which the helper below reports as "the Python is not importable"
and skips. Two comparisons skipped in silence is worse than either of them
failing.
*/
var realHome = os.Getenv("HOME")

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a fixed script
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+realHome)
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

// pyPairs is a list of address pairs as a Python literal.
func pyPairs(pairs [][2]uint32) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("(%d, %d)", p[0], p[1])
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// pyInts is a list of addresses as a Python literal.
func pyInts(v []uint32) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// bytes is n copies of one byte, for planting a run of padding.
func bytes(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// pyBytes is a byte string as a Python literal.
func pyBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return "bytes([" + strings.Join(parts, ", ") + "])"
}

// pyI32 is a list of signed values as a Python literal.
func pyI32(v []int32) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// pyCalls is a list of (anchor, offset) pairs as a Python literal.
func pyCalls(cases []struct {
	anchor string
	off    int
}) string {
	parts := make([]string, len(cases))
	for i, c := range cases {
		parts[i] = fmt.Sprintf("(%q, %d)", c.anchor, c.off)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// u32 is an address as the game stores it.
func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

/*
localPlayerTail is the JIT'd shape of Main.get_LocalPlayer, which two of the
stubs resolve the live player through.

The locate package pins these bytes against the Python; here they are only
planting material, so that the stubs have something to find.
*/
var localPlayerTail = []byte{
	0x39, 0x48, 0x0C, 0x0F, 0x86, 0x07, 0x00, 0x00, 0x00,
	0x8D, 0x44, 0x88, 0x10, 0x8B, 0x00, 0xC3,
}
