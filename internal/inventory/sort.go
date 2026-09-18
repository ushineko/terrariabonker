package inventory

import (
	"maps"
	"slices"
)

// sortedKeys is a map's keys in order, which is how the Python reports the same
// list and so is what a caller comparing the two sees.
func sortedKeys(m map[string]float64) []string {
	return nonNil(slices.Sorted(maps.Keys(m)))
}

// sortedSet is the same for a set.
func sortedSet(m map[string]bool) []string {
	return nonNil(slices.Sorted(maps.Keys(m)))
}

// nonNil is an empty list rather than no list. The Python reports nothing
// skipped as an empty list, and a caller reading JSON has to see the same thing
// from both.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
