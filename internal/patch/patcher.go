package patch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ushineko/terrariabonker/internal/locate"
)

/*
Patcher applies and removes the code patches on one running game.

This is the only thing in the project that writes instructions into another
process. Everything it does is guarded twice -- the bytes at a site are checked
against what is expected before a jump goes over them, and an arena slot is
checked for being empty before a stub goes into it -- because being wrong here
does not fail. The game runs the wrong instructions and dies later, somewhere
with no connection to the trainer.
*/
type Patcher struct {
	Mem     BuilderMem
	PID     int
	Scanner *Scanner

	// OnChange is told about every toggle, so a caller can record it wherever it
	// keeps the player's profile. The patcher does not know what a profile is.
	OnChange func(name string, on bool, value float64)

	// OnWait is called once the arena bootstrap is hooked and waiting, so a
	// caller can say it is waiting rather than appearing to hang.
	OnWait func()

	state State
	arena uint32
}

/*
ErrNotAPatch is what a name no table declares comes back as.

Distinguished from every other failure because the two mean opposite things to a
caller putting a saved configuration back: a patch that will not apply right now
is worth retrying, while a name this version of the program has never heard of
is not going to start existing.
*/
var ErrNotAPatch = errors.New("not a patch")

// NewPatcher is a patcher over a running game.
func NewPatcher(mem BuilderMem, pid int) *Patcher {
	p := &Patcher{Mem: mem, PID: pid, Scanner: NewScanner(mem)}
	p.state = LoadState(pid)
	p.adoptArena()
	return p
}

/*
adoptArena takes up an arena this process was already given, and tells the
scanner to keep out of it.

The record is consulted first and the game second. Looking in the game at all is
this port's own: the Python waits until something asks for an arena, which leaves
a window where the record has been lost -- a different pid, a deleted file -- and
the scanner does not yet know to skip a region full of live stubs. A cave search
in that window can hand out space one of them is running in, which is the exact
failure the skip exists to prevent. Finding it here costs one pass over the
mappings and closes the window.

It never *allocates*. That needs the game to be running frames and is the
caller's decision, not a side effect of constructing a patcher.
*/
func (p *Patcher) adoptArena() {
	if p.state.Arena != 0 && ArenaOK(p.Mem, p.state.Arena) {
		p.setArena(p.state.Arena)
		return
	}
	if found, ok := FindArena(p.Mem); ok {
		p.setArena(found)
	}
}

// setArena records the arena and excludes it from every scan. Memory this
// program put something in is never padding.
func (p *Patcher) setArena(base uint32) {
	p.arena = base
	p.Scanner.Skip = [2]uint32{base, base + ArenaSize}
}

// Status is whether each patch is applied, read from the game.
func (p *Patcher) Status() map[string]bool {
	out := make(map[string]bool, len(Cheats)+len(Injections))
	for _, info := range Catalog() {
		out[info.Name] = isEnabledWith(p.Scanner, p.Mem, info.Name, p.state)
	}
	return out
}

// IsEnabled is that question for one patch.
func (p *Patcher) IsEnabled(name string) bool {
	return isEnabledWith(p.Scanner, p.Mem, name, p.state)
}

// Values are the values each cheat was last given.
func (p *Patcher) Values() map[string]float64 { return p.state.Values }

/*
Enable applies a patch, and records the value it was given.

Under the state lock, so concurrent toggles serialise on the shared record: the
window applies several cheats one process at a time, and without it the second
saves a record that does not know about the first.
*/
func (p *Patcher) Enable(name string, value *float64) error {
	return p.locked(func(s *State) error {
		if inj, ok := Injections[name]; ok {
			v := valueFor(name, value, 30)
			if err := p.enableInjection(s, inj, v); err != nil {
				return err
			}
			s.SetEnabled(name, true)
			p.recordValue(s, name, v)
			p.changed(name, true, s.Values[name])
			return nil
		}
		cheat, ok := Cheats[name]
		if !ok {
			return fmt.Errorf("%q is %w", name, ErrNotAPatch)
		}
		res := p.Scanner.Resolve(cheat.Anchor, "")
		if !res.Available {
			return fmt.Errorf("%s", res.Reason)
		}
		/*
			Every copy is patched. mono can JIT one method into more than one
			arena and which copy executes is not knowable; the copies are
			identical where this writes, so a stale one is inert and the live one
			takes effect.
		*/
		if cheat.Tunable() {
			v := valueFor(name, value, 1)
			blob := cheat.MakePatched(int32(v))
			for _, base := range res.Sites {
				p.Mem.Write(offsetBy(base, cheat.PatchOff), blob)
			}
			p.recordValue(s, name, v)
		} else {
			for _, base := range res.Sites {
				p.Mem.Write(offsetBy(base, cheat.PatchOff), cheat.Patched)
			}
			if err := p.setValue(cheat, true, value); err != nil {
				return err
			}
			p.recordValueOrDefault(s, name, value)
		}
		s.SetEnabled(name, true)
		p.changed(name, true, s.Values[name])
		return nil
	})
}

// Disable removes a patch and puts back what was there.
func (p *Patcher) Disable(name string) error {
	return p.locked(func(s *State) error {
		if inj, ok := Injections[name]; ok {
			if err := p.disableInjection(s, inj); err != nil {
				return err
			}
			s.SetEnabled(name, false)
			p.changed(name, false, 0)
			return nil
		}
		cheat, ok := Cheats[name]
		if !ok {
			return fmt.Errorf("%q is %w", name, ErrNotAPatch)
		}
		res := p.Scanner.Resolve(cheat.Anchor, "")
		if !res.Available {
			return fmt.Errorf("%s", res.Reason)
		}
		for _, base := range res.Sites {
			p.Mem.Write(offsetBy(base, cheat.PatchOff), cheat.Orig)
		}
		if !cheat.Tunable() {
			if err := p.setValue(cheat, false, nil); err != nil {
				return err
			}
		}
		s.SetEnabled(name, false)
		p.changed(name, false, 0)
		return nil
	})
}

/*
enableInjection writes a stub and points the site at it.

A re-apply reuses the recorded sites and caves and only rewrites the stub, for a
live value change. The anchor must *not* be resolved again on that path: some
injection sites overlap their own anchor bytes, so a pristine scan finds nothing
once the jump is in place. A fresh resolve happens only on a first enable.
*/
func (p *Patcher) enableInjection(s *State, inj Injection, value float64) error {
	body, err := p.stubBody(inj, value)
	if err != nil {
		return err
	}
	stubLen := len(body) + 5 // and the jump home

	prev := s.Inj[inj.Name]
	var injects, caves []uint32
	first := len(prev.Sites) == 0
	if !first {
		for _, site := range prev.Sites {
			injects = append(injects, site.Inject)
			caves = append(caves, site.Cave)
		}
	} else {
		bases, err := p.sitesFor(inj)
		if err != nil {
			return err
		}
		for i, base := range bases {
			injects = append(injects, offsetBy(base, inj.InjectOff))
			cave, err := p.caveFor(inj, i, stubLen, caves, *s)
			if err != nil {
				return err
			}
			caves = append(caves, cave)
		}
	}

	var sites []Site
	for i, inject := range injects {
		cave := caves[i]
		// The jump home lands on the byte after the ones displaced.
		back := inject + uint32(len(inj.Overwrite)) //nolint:gosec // a short overwrite
		stub := append(append([]byte{}, body...), 0xE9)
		stub = append(stub, Rel32(cave+uint32(stubLen), back)...) //nolint:gosec // a short stub

		/*
			Only on a first enable. A re-apply is writing over its own jump, and
			by then the displaced bytes live in the stub rather than at the site,
			so both checks would refuse work that is correct.
		*/
		if first {
			if err := CheckSite(p.Mem, inject, inj.Overwrite, "enable "+inj.Name); err != nil {
				return err
			}
			if inj.Arena {
				if err := CheckSlot(p.Mem, cave, stubLen, "enable "+inj.Name); err != nil {
					return err
				}
			}
		}
		p.Mem.Write(cave, stub)
		p.Mem.Write(inject, append([]byte{0xE9}, Rel32(inject+5, cave)...))
		sites = append(sites, Site{Inject: inject, Cave: cave})
	}

	if err := p.applyEdits(inj.Edits, true); err != nil {
		return err
	}
	s.Inj[inj.Name] = Installed{Sites: sites, StubLen: stubLen}
	return nil
}

// stubBody is the instructions a stub runs, however this injection builds them.
func (p *Patcher) stubBody(inj Injection, value float64) ([]byte, error) {
	switch {
	case inj.BuildBody != nil: // built from live state
		arena, err := p.Arena()
		if err != nil {
			return nil, err
		}
		body, err := inj.BuildBody(&Builder{Scanner: p.Scanner, Mem: p.Mem, Arena: arena}, inj)
		if err != nil {
			return nil, err
		}
		if inj.Name == "auto_use" {
			// The words the stub reads, set up before it can run. Never inherit
			// a pending press from a past run.
			p.Mem.Write(arena+AutoUseReleaseOff, i32(ReleaseItemOff))
			p.Mem.Write(arena+AutoUseArmedOff, i32(0))
		}
		return body, nil

	case inj.CallAnchor != "": // a stub that calls back into the game
		res := p.Scanner.Resolve(inj.CallAnchor, "")
		if !res.Available {
			return nil, fmt.Errorf("%s", res.Reason)
		}
		target := res.Sites[0] - uint32(inj.CallTargetOff) //nolint:gosec // a fixed offset
		/*
			The authoritative player, not the first one a scan finds: the scan
			also returns inert load-time snapshots, and a stub built around one
			of those teleports an object the game does not read.
		*/
		blk, ok := locate.ResolveLocalPlayer(p.Mem)
		if !ok {
			found := locate.FindPlayers(p.Mem)
			if len(found) == 0 {
				return nil, fmt.Errorf("no player found")
			}
			blk = found[0]
		}
		return TeleportBody(blk.LifeAddr-locate.StatLifeFromObj, target), nil

	case inj.MakeBody != nil:
		body := inj.MakeBody(int32(value))
		if inj.RerunOverwrite {
			body = append(body, inj.Overwrite...) // then continue what was displaced
		}
		return body, nil
	}
	return nil, fmt.Errorf("%s has no way to build its stub", inj.Name)
}

/*
sitesFor is every place this injection is installed.

An injection marked multi is expected to match structural twins and goes into all
of them; everything else takes the first match, since identical JIT copies are
interchangeable.
*/
func (p *Patcher) sitesFor(inj Injection) ([]uint32, error) {
	if inj.Multi {
		/*
			Deliberately not cached: one scan per enable is cheap and the set of
			sites can differ across a re-JIT.
		*/
		hits := p.Scanner.Scan(inj.Anchor, "")
		if len(hits) == 0 {
			return nil, fmt.Errorf("%s", p.Scanner.Resolve(inj.Anchor, "").Reason)
		}
		return hits, nil
	}
	res := p.Scanner.Resolve(inj.Anchor, "")
	if !res.Available {
		return nil, fmt.Errorf("%s", res.Reason)
	}
	return res.Sites[:1], nil
}

// caveFor is where one site's stub goes: an arena slot, or borrowed padding.
func (p *Patcher) caveFor(inj Injection, site, stubLen int, claimed []uint32, s State) (uint32, error) {
	if !inj.Arena {
		return FindCave(p.Scanner, stubLen, claimed, inj.WritesCave, s)
	}
	if stubLen > ArenaSlot {
		return 0, fmt.Errorf("%s: stub is %d bytes, larger than the %d-byte arena slot",
			inj.Name, stubLen, ArenaSlot)
	}
	arena, err := p.Arena()
	if err != nil {
		return 0, err
	}
	return SlotFor(arena, inj.Name, site)
}

// disableInjection puts the displaced bytes back and scrubs the stub.
func (p *Patcher) disableInjection(s *State, inj Injection) error {
	rec, known := s.Inj[inj.Name]
	if !known || len(rec.Sites) == 0 {
		// No record, which is rare: resolve every site again and restore those.
		bases, err := p.sitesFor(inj)
		if err != nil {
			return err
		}
		rec = Installed{}
		for _, base := range bases {
			rec.Sites = append(rec.Sites, Site{Inject: offsetBy(base, inj.InjectOff)})
		}
	}
	for _, site := range rec.Sites {
		p.Mem.Write(site.Inject, inj.Overwrite)
		if site.Cave != 0 && rec.StubLen > 0 {
			p.Mem.Write(site.Cave, bytes.Repeat([]byte{0xCC}, rec.StubLen))
		}
	}
	if err := p.applyEdits(inj.Edits, false); err != nil {
		return err
	}
	delete(s.Inj, inj.Name)
	return nil
}

// applyEdits writes each edit at every site its anchor resolves to, because a
// method can be JIT'd into more than one arena.
func (p *Patcher) applyEdits(edits []Edit, on bool) error {
	for _, e := range edits {
		res := p.Scanner.Resolve(e.Anchor, "")
		if !res.Available {
			return fmt.Errorf("%s", res.Reason)
		}
		want := e.Orig
		if on {
			want = e.Patched
		}
		for _, base := range res.Sites {
			p.Mem.Write(offsetBy(base, e.Off), want)
		}
	}
	return nil
}

/*
setValue writes the player field a cheat carries, in every copy.

Every copy for the same reason the code sites are: which one the game reads is
not knowable here, and the inert ones ignore what lands on them.
*/
func (p *Patcher) setValue(cheat Cheat, on bool, override *float64) error {
	if cheat.ValueOff == 0 {
		return nil
	}
	val := cheat.OffValue
	if on {
		val = cheat.OnValue
		if override != nil {
			val = *override
		}
	}
	var raw []byte
	if cheat.ValueF32 {
		raw = binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(val)))
	} else {
		raw = i32(int32(val))
	}
	blocks := locate.FindPlayers(p.Mem)
	if len(blocks) == 0 {
		return fmt.Errorf("no player found")
	}
	for _, b := range blocks {
		p.Mem.Write(offsetBy(b.LifeAddr, cheat.ValueOff), raw)
	}
	return nil
}

// valueFor is the value to use: the caller's, else the cheat's own default, else
// the fallback this patch was written with.
func valueFor(name string, value *float64, fallback float64) float64 {
	if value != nil {
		return *value
	}
	if spec, ok := ValueSpecs[name]; ok {
		return spec.Default
	}
	return fallback
}

// recordValue remembers what a cheat was last set to, so the window can offer it
// again.
func (p *Patcher) recordValue(s *State, name string, value float64) {
	if _, ok := ValueSpecs[name]; ok {
		s.Values[name] = value
	}
}

// recordValueOrDefault is the same for a cheat whose value may not have been
// given.
func (p *Patcher) recordValueOrDefault(s *State, name string, value *float64) {
	spec, ok := ValueSpecs[name]
	if !ok {
		return
	}
	if value == nil {
		s.Values[name] = spec.Default
		return
	}
	s.Values[name] = *value
}

// changed tells the caller about a toggle, if it asked to be told.
func (p *Patcher) changed(name string, on bool, value float64) {
	if p.OnChange != nil {
		p.OnChange(name, on, value)
	}
}

/*
locked runs a mutating operation with the record held and re-read.

The in-memory state is replaced by what the lock hands over, so an operation
never works from a record another process has since written.
*/
func (p *Patcher) locked(do func(*State) error) error {
	return WithLock(p.PID, func(s *State) error {
		s.Arena = p.arena
		p.state = *s
		if err := do(s); err != nil {
			return err
		}
		s.Arena = p.arena
		p.state = *s
		return nil
	})
}

/*
Arena is memory of this program's own inside the game.

Adopted if one was made earlier, and otherwise the game is asked to allocate a
fresh one. It needs the game to be *running*: the bootstrap hangs on a per-frame
path, and Terraria pauses in single-player whenever its window loses focus, so a
paused game runs no frames and nothing happens.
*/
func (p *Patcher) Arena() (uint32, error) { return p.ArenaWithin(20 * time.Second) }

/*
ArenaReady reports whether the arena is already there, without allocating one.

Asked by the caller that allocates opportunistically: allocating needs the game
to be running frames, so it is worth knowing there is nothing to do before
looking at whether it is.
*/
func (p *Patcher) ArenaReady() bool {
	return p.arena != 0 && ArenaOK(p.Mem, p.arena)
}

/*
ArenaWithin is Arena with a bound on how long the springboard may wait.

The default is generous because a person who just asked for a cheat will wait.
The opportunistic caller will not: it runs on a poll, and a game that is paused
now will be polled again in a moment.
*/
func (p *Patcher) ArenaWithin(wait time.Duration) (uint32, error) {
	if p.ArenaReady() {
		return p.arena, nil
	}
	if found, ok := FindArena(p.Mem); ok {
		p.setArena(found)
		return found, nil
	}

	base, err := FreeBase(p.Mem, ArenaSize)
	if err != nil {
		return 0, err
	}
	if err := p.bootstrapArena(base, wait); err != nil {
		return 0, err
	}
	region, ok := Mapped(p.Mem, base)
	if !ok {
		return 0, fmt.Errorf("VirtualAlloc did not run. The springboard sits on a " +
			"per-frame path, so this means the game is not advancing frames -- Terraria " +
			"pauses in single-player whenever its window loses focus. Focus the game " +
			"and try again")
	}
	if !region.Writable || !region.Executable {
		return 0, fmt.Errorf("the arena at %#x is not writable and executable", base)
	}
	p.Mem.Write(base+ArenaMagicOff, ArenaMagic)
	p.setArena(base)
	return base, nil
}
