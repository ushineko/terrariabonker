package version_test

import "sort"

// sortedKeys is a map's keys in order, so a planting plan is laid out the same
// way twice.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
