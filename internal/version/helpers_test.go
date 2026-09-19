package version_test

import (
	"fmt"
	"sort"
	"strings"
)

// pyPairs is a list of string pairs as a Python literal.
func pyPairs(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("(%q, %q)", p[0], p[1])
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// pyPlant is a planting plan as a Python literal, in a fixed order so both sides
// write the same bytes to the same places.
func pyPlant(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, text := range sortedKeys(counts) {
		parts = append(parts, fmt.Sprintf("(%q, %d)", text, counts[text]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// sortedKeys is a map's keys in order, so a plan is planted the same way twice.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
