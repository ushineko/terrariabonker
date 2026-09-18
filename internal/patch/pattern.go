/*
Package patch is the code patching half of the trainer: the byte patterns that
find the game's own machine code, and the provenance of each one.

This is the module where being wrong costs more than an error message. Every
other package reads or writes a number the game owns; this one writes
instructions into a running process, and a pattern that matches the wrong place
corrupts the game rather than reporting a failure.

So the patterns move across as *data*, not as rewritten code, and every one of
them is compared against the Python's byte for byte while both exist.

Ported from terrariabonker/patcher.py (spec 051, step 7).
*/
package patch

import (
	"fmt"
	"strconv"
	"strings"
)

/*
Pattern is a byte sequence to find in the game's code, with wildcards.

Raw holds the bytes, zero where a byte is wildcarded, and Mask is true at the
fixed positions. Wildcards are what make an anchor survive the things that
change between runs and between builds: absolute addresses, mono's type-init
thunks, call displacements -- and, deliberately, the bytes a cheat itself
overwrites, so an anchor still resolves once its own patch is applied.
*/
type Pattern struct {
	Raw  []byte
	Mask []bool
}

/*
MustParse is a pattern from its written form, where "??" is a wildcard.

It panics on anything else, because these are constants in this package's own
source: a malformed one is a bug that should stop the program at startup rather
than turn into an anchor that never matches.
*/
func MustParse(s string) Pattern {
	pat, err := Parse(s)
	if err != nil {
		panic("patch: " + err.Error())
	}
	return pat
}

// Parse is a pattern from its written form.
func Parse(s string) (Pattern, error) {
	tokens := strings.Fields(s)
	pat := Pattern{Raw: make([]byte, len(tokens)), Mask: make([]bool, len(tokens))}
	for i, tok := range tokens {
		if tok == "??" {
			continue
		}
		v, err := strconv.ParseUint(tok, 16, 8)
		if err != nil {
			return Pattern{}, fmt.Errorf("byte %d of the pattern is %q", i, tok)
		}
		pat.Raw[i], pat.Mask[i] = byte(v), true
	}
	return pat, nil
}

// Len is how many bytes the pattern covers, wildcards included.
func (p Pattern) Len() int { return len(p.Raw) }

/*
Seed is the longest run of fixed bytes, and where it starts.

A scan looks for this with a plain substring search and only then checks the
whole pattern, so the run has to be the longest one: a two-byte seed in a
gigabyte of code is millions of candidate positions to check.
*/
func (p Pattern) Seed() (int, []byte) {
	bestOff, bestLen, curOff, curLen := 0, 0, 0, 0
	for i, fixed := range p.Mask {
		if !fixed {
			curLen = 0
			continue
		}
		if curLen == 0 {
			curOff = i
		}
		curLen++
		if curLen > bestLen {
			bestLen, bestOff = curLen, curOff
		}
	}
	return bestOff, p.Raw[bestOff : bestOff+bestLen]
}

// Matches reports whether the pattern is at pos in buf, wildcards ignored.
func (p Pattern) Matches(buf []byte, pos int) bool {
	if pos < 0 || pos+len(p.Raw) > len(buf) {
		return false
	}
	for j, fixed := range p.Mask {
		if fixed && buf[pos+j] != p.Raw[j] {
			return false
		}
	}
	return true
}

// String is the pattern in the form it was written in, so a failure can name it
// and a test can compare the two implementations without a byte-by-byte dance.
func (p Pattern) String() string {
	parts := make([]string, len(p.Raw))
	for i, b := range p.Raw {
		if p.Mask[i] {
			parts[i] = fmt.Sprintf("%02X", b)
		} else {
			parts[i] = "??"
		}
	}
	return strings.Join(parts, " ")
}

// Equal reports whether two patterns are the same bytes and the same wildcards.
func (p Pattern) Equal(other Pattern) bool {
	if len(p.Raw) != len(other.Raw) {
		return false
	}
	for i := range p.Raw {
		if p.Mask[i] != other.Mask[i] || (p.Mask[i] && p.Raw[i] != other.Raw[i]) {
			return false
		}
	}
	return true
}
