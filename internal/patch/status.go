package patch

import "bytes"

/*
Whether a cheat is on is answered by reading the game, not by trusting the
record.

The record can be wrong -- a state file lost, a game patched by some other run,
two invocations racing -- and the window shows this to somebody about to toggle
it. So the bytes at the site are the answer, and the anchors wildcard the bytes a
cheat overwrites precisely so that they still resolve either way.

A cold cache is not evidence of anything. This used to report every injection off
when neither the record nor the site cache had an entry, so a fresh process said
nothing was installed no matter what was in the game -- and the caches are cold in
exactly the case where the answer matters, a run against a game some other
process patched.
*/

// IsEnabled reports whether a patch is applied, by reading the game.
func IsEnabled(sc *Scanner, mem Mem, name string) bool {
	return isEnabledWith(sc, mem, name, State{Inj: map[string]Installed{}})
}

// isEnabledWith is the same, given a record that may already know where the
// stubs went.
func isEnabledWith(sc *Scanner, mem Mem, name string, state State) bool {
	if inj, ok := Injections[name]; ok {
		return injectionEnabled(sc, mem, inj, state)
	}
	cheat, ok := Cheats[name]
	if !ok {
		return false
	}
	res := sc.Resolve(cheat.Anchor, "")
	if !res.Available {
		return false // not compiled yet: there is nothing to be on
	}
	/*
		Any patched copy counts as on. Which copy the game executes is not
		knowable, and disabling reverts them all, so "any" and "all" differ only
		part-way through an operation.
	*/
	for _, base := range res.Sites {
		site := base + uint32(cheat.PatchOff) //nolint:gosec // an offset inside a match
		if cheat.Tunable() {
			// A tunable cheat is on when the site is anything but the original:
			// what it was set to is a value, not a second thing to recognise.
			if !bytes.Equal(mem.Read(site, len(cheat.Orig)), cheat.Orig) {
				return true
			}
			continue
		}
		if bytes.Equal(mem.Read(site, len(cheat.Patched)), cheat.Patched) {
			return true
		}
	}
	return false
}

// injectionEnabled is the same question for a stub: is there a jump at the site.
func injectionEnabled(sc *Scanner, mem Mem, inj Injection, state State) bool {
	var sites []uint32
	if rec, ok := state.Inj[inj.Name]; ok && len(rec.Sites) > 0 {
		for _, s := range rec.Sites {
			sites = append(sites, s.Inject)
		}
	} else {
		res := sc.Resolve(inj.Anchor, "")
		if !res.Available {
			return false
		}
		for _, base := range res.Sites {
			sites = append(sites, offsetBy(base, inj.InjectOff))
		}
	}
	for _, s := range sites {
		if b := mem.Read(s, 1); len(b) == 1 && b[0] == 0xE9 {
			return true
		}
	}
	return false
}

// offsetBy moves an address by an offset that may be negative: an injection
// point can sit in front of the pattern that found it.
func offsetBy(addr uint32, off int) uint32 {
	return uint32(int64(addr) + int64(off)) //nolint:gosec // a 32-bit address, deliberately
}
