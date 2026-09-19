package patch

import (
	"bytes"
	"fmt"

	"github.com/ushineko/terrariabonker/internal/proc"
)

// Mem is the memory a scan reads: the regions it may look in, and their bytes.
type Mem interface {
	ExecRegions() []proc.Region
	Read(addr uint32, size int) []byte
}

/*
Resolution is what resolving one anchor produced: where it matched, whether that
is usable, and why not when it is not.

The reason states what was observed and nothing more. An anchor that matched
nothing may have moved in this build or may simply not be JIT-compiled yet,
which is a different problem with the same symptom, and guessing between them in
the message sends the reader the wrong way.
*/
type Resolution struct {
	Sites     []uint32
	Available bool
	Reason    string
	Verified  bool
}

/*
Scanner finds anchors in a running game's code.

Sites once resolved are kept: the scan reads every executable mapping, which is
over a gigabyte, and the addresses do not move while the process lives unless
mono re-JITs.
*/
type Scanner struct {
	Mem Mem
	// Skip is a range the scan will not look in. It is this program's own arena:
	// that memory is RWX, comes back zero-filled, and disabling a stub scrubs it
	// to 0xCC, so to anything looking for unused padding it is the most
	// attractive space in the process. It once handed a slice of the extractor's
	// own stub to the next injection enabled, one wrote over the other, and the
	// game died executing the splice. Memory this program put something in is
	// never padding.
	Skip [2]uint32

	sites map[string][]uint32
}

// NewScanner is a scanner over a game's memory.
func NewScanner(mem Mem) *Scanner {
	return &Scanner{Mem: mem, sites: map[string][]uint32{}}
}

/*
Regions is the executable mappings a scan may look in.

writable keeps only the ones the CPU may also write to, which is almost none of
them: a code cave is borrowed padding inside somebody else's read-execute
mapping.
*/
func (s *Scanner) Regions(writable bool) []proc.Region {
	var out []proc.Region
	for _, r := range s.Mem.ExecRegions() {
		if writable && !r.Writable {
			continue
		}
		if s.Skip[1] != 0 && r.Start < s.Skip[1] && s.Skip[0] < r.End {
			continue
		}
		out = append(out, r)
	}
	return out
}

/*
Scan is every address an anchor matches.

It searches for the pattern's longest fixed run and only then checks the whole
pattern, because a wildcarded pattern cannot be searched for directly and
checking every position in a gigabyte would not finish.

An anchor may carry per-build variants, and each is tried in turn until one
matches. Trying them all is deliberate: see Anchor.Candidates.
*/
func (s *Scanner) Scan(anchorKey, build string) []uint32 {
	anchor, known := Anchors[anchorKey]
	if !known {
		return nil
	}
	var regions []region
	for _, pat := range anchor.Candidates(build) {
		if regions == nil {
			regions = s.read()
		}
		seedOff, seed := pat.Seed()
		var hits []uint32
		for _, r := range regions {
			for i := 0; ; {
				at := bytes.Index(r.buf[i:], seed)
				if at < 0 {
					break
				}
				at += i
				if pos := at - seedOff; pat.Matches(r.buf, pos) {
					hits = append(hits, r.start+uint32(pos)) //nolint:gosec // an offset in a 32-bit region
				}
				i = at + 1
			}
		}
		if len(hits) > 0 {
			return hits
		}
	}
	return nil
}

// region is one mapping and its contents, read once and searched for every
// candidate pattern.
type region struct {
	start uint32
	buf   []byte
}

// read pulls in every region the scan may look at.
func (s *Scanner) read() []region {
	regions := s.Regions(false)
	out := make([]region, 0, len(regions))
	for _, r := range regions {
		out = append(out, region{start: r.Start, buf: s.Mem.Read(r.Start, r.Size())})
	}
	return out
}

/*
Resolve is an anchor's sites, and whether they can be used.

Several matches is normal and not an error: mono can JIT one method into more
than one arena, and the copies are identical where this patches. Only an anchor
declared unique treats that as a failure.
*/
func (s *Scanner) Resolve(anchorKey, build string) Resolution {
	anchor := Anchors[anchorKey]
	verified := build != "" && contains(anchor.Verified, build)

	sites := s.sites[anchorKey]
	if len(sites) == 0 {
		sites = s.Scan(anchorKey, build)
	}
	switch {
	case len(sites) == 0:
		return Resolution{Available: false, Verified: verified, Reason: fmt.Sprintf(
			"anchor %q matched nothing -- the method may not be JIT-compiled yet, "+
				"or this build has moved it", anchorKey)}
	case anchor.Unique && len(sites) > 1:
		return Resolution{Sites: sites, Available: false, Verified: verified,
			Reason: fmt.Sprintf("anchor %q matched %d sites but must be unique",
				anchorKey, len(sites))}
	}
	s.sites[anchorKey] = sites
	return Resolution{Sites: sites, Available: true, Verified: verified}
}

/*
Cached is the sites an anchor resolved to earlier in this session, if any.

Asked rather than resolved when the answer would otherwise come from a scan that
is no longer meaningful: once an injection is applied its jump sits on top of its
own anchor, so a fresh scan finds nothing and the count the window shows would
drop to zero for a cheat that is working.
*/
func (s *Scanner) Cached(anchorKey string) []uint32 { return s.sites[anchorKey] }

// contains reports whether a list has a string in it.
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
