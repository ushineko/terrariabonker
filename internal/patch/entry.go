package patch

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
The patcher's numbers come from the version-table entry it is given (spec 052).

Everything that varies by build or runtime is selected by name, never branched
on: an entry's in-place cheats and their anchors are data on the entry; the
encoders that turn a value into bytes are a registry keyed by the name a cheat
site gives; and an entry's stub-based cheats are the injection set it names. A
new runtime, or a new version of one, is a new entry and, where it needs code, a
new set -- not another case in the code that applies them.
*/

// encoders turn a cheat's value into the bytes its site writes, by the name a
// layout.CheatSite gives. They are x86 encodings, not runtime facts.
var encoders = map[string]func(int32) []byte{
	// A 4-byte immediate, at least 1: max_minions' `maxMinions = N`.
	"imm32-min1": func(n int32) []byte { return i32(max32(n, 1)) },
	// `mov edi, N` (N at least 1) and five nops, over a 10-byte clamp:
	// fast_place's item time.
	"mov-edi-imm32-min1": func(n int32) []byte {
		return append(append([]byte{0xBF}, i32(max32(n, 1))...), 0x90, 0x90, 0x90, 0x90, 0x90)
	},
	// `mov eax, N` (N at least 1) and ten nops, over a 15-byte clamp: fast_place's
	// item time where the clamp leaves the time in eax.
	"mov-eax-imm32-min1": func(n int32) []byte {
		return append(append([]byte{0xB8}, i32(max32(n, 1))...), bytes.Repeat([]byte{0x90}, 10)...)
	},
}

/*
holds reports whether bytes read at a site are what this cheat expects to find
there: the original, or the cheat's own patch -- for a cheat built from a value,
the encoder's output for some value, recognised by every byte the value does not
change.

Anchors wildcard exactly the bytes a cheat writes, so that they resolve with the
cheat on. That makes a match no evidence of what those bytes are, and a write
guarded only by the anchor would overwrite whatever an unexpected match holds.
*/
func holds(site layout.CheatSite, cur []byte) bool {
	if len(cur) != len(site.Orig) {
		return false
	}
	if bytes.Equal(cur, site.Orig) {
		return true
	}
	if site.Encoder == "" {
		return bytes.Equal(cur, site.Patched)
	}
	enc := encoders[site.Encoder]
	a, b := enc(0x01010101), enc(0x02020202)
	if len(a) != len(cur) {
		return false
	}
	for i := range cur {
		if a[i] == b[i] && cur[i] != a[i] {
			return false
		}
	}
	return true
}

/*
injectionSet is one implementation of the stub-based cheats: their anchors and
their injections. Stubs are code written for one runtime's compiled output --
its registers, its calling convention -- so each runtime that has them has its
own set, and an entry names the one it uses.
*/
type injectionSet struct {
	anchors    map[string]Anchor
	injections map[string]Injection
}

// injectionSets is every set, by the name a layout.Entry gives in Injections.
// The mono set is the tables in anchors.go and injections.go.
var injectionSets = map[string]injectionSet{
	"mono": {anchors: Anchors, injections: Injections},
}

/*
UseEntry points the patcher at a version-table entry: its in-place cheats and
their anchors, its injection set, and the player fields its cheats set. A
patcher is made with the mono entry, which is what every patcher was before the
table.
*/
func (p *Patcher) UseEntry(e layout.Entry) {
	p.entry = e
	p.set = injectionSets[e.Injections]
	p.anchors = AnchorsFor(e)
	p.Scanner.Lookup = func(key string) (Anchor, bool) {
		a, ok := p.anchors[key]
		return a, ok
	}
}

/*
AnchorsFor is every anchor a patcher with this entry resolves through: the
entry's own, which its in-place cheats use, and those of the injection set it
names. A key is declared in one or the other, never both.
*/
func AnchorsFor(e layout.Entry) map[string]Anchor {
	set := injectionSets[e.Injections]
	out := make(map[string]Anchor, len(e.Anchors)+len(set.anchors))
	for key, a := range set.anchors {
		out[key] = a
	}
	for key, def := range e.Anchors {
		out[key] = Anchor{Pattern: MustParse(def.Pattern), Unique: def.Unique, Verified: def.Verified}
	}
	return out
}

// cheatSite is where and what an in-place cheat writes under the entry.
func (p *Patcher) cheatSite(name string) (layout.CheatSite, bool) {
	site, ok := p.entry.Cheats[name]
	return site, ok
}

// injection is a stub-based cheat in the entry's injection set.
func (p *Patcher) injection(name string) (Injection, bool) {
	inj, ok := p.set.injections[name]
	return inj, ok
}

// anchorKey is the anchor a patch resolves through under the entry, or "".
func (p *Patcher) anchorKey(name string) string {
	if inj, ok := p.injection(name); ok {
		return inj.Anchor
	}
	return p.entry.Cheats[name].Anchor
}

// anchorDef is an anchor under the entry, for its verification ledger.
func (p *Patcher) anchorDef(key string) Anchor { return p.anchors[key] }

/*
inPlace is an in-place cheat and its site under the entry.

A name no table declares is not a patch (ErrNotAPatch: it will not start
existing). A cheat the catalog has but this entry gives no site is a different
answer -- it exists, and this runtime has no code for it yet -- and saying so is
what tells a person on Windows that a cheat is missing rather than broken.
*/
func (p *Patcher) inPlace(name string) (Cheat, layout.CheatSite, error) {
	cheat, known := Cheats[name]
	_, isInjection := Injections[name] // the catalog's injection names
	if !known && !isInjection {
		return Cheat{}, layout.CheatSite{}, fmt.Errorf("%q is %w", name, ErrNotAPatch)
	}
	site, ok := p.cheatSite(name)
	if !known || !ok {
		return Cheat{}, layout.CheatSite{}, errors.New(p.noSite(name))
	}
	return cheat, site, nil
}

// noSite is why a cheat the catalog has cannot be applied under the entry.
func (p *Patcher) noSite(name string) string {
	return fmt.Sprintf("%s has no code site under %s yet (spec 052)", name, p.entry.Name)
}

// AnchorKey is the anchor a patch resolves through under the patcher's entry,
// or "" when the entry has none for it.
func (p *Patcher) AnchorKey(name string) string { return p.anchorKey(name) }
