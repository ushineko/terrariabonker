package tiles_test

import "sort"

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func u16(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }

// sortedKeys is the planted coordinates in a fixed order, so a world is laid
// out the same way twice.
func sortedKeys[V any](m map[[2]int32]V) [][2]int32 {
	out := make([][2]int32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
