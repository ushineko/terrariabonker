package tiles_test

import "sort"

func u32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func u16(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }

// nilOr is a value the Python would have written as null when it was not there.
func nilOr(v uint16, present bool) any {
	if !present {
		return nil
	}
	return float64(v)
}

// nilOrBool is the same for a flag.
func nilOrBool(v, present bool) any {
	if !present {
		return nil
	}
	return v
}

// pyBool is a flag as Python spells it.
func pyBool(v bool) string {
	if v {
		return "True"
	}
	return "False"
}

// sortedKeys is the planted coordinates in a fixed order, so both sides write
// them the same way.
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
