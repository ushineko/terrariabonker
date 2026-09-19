package projectile_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/memtest"
)

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// memtest0 is the fake this package's fixtures build, named so the projectile
// tests can pass it around.
type memtest0 = memtest.FakeMem

// pyFloats is a list of numbers as a Python literal.
func pyFloats(v []float64) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.FormatFloat(n, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
