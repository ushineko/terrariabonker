/*
Package player is a handle on one player copy in the game's memory.

A handle is an address and nothing else: every field is an offset from
Player.statLife, which is the address the locator anchors on, so the whole map
survives the managed heap moving the object as a unit. Nothing is cached,
because the game is writing to these fields continuously and a value read a
frame ago is already old.

Which copy the address belongs to is the caller's problem, and it matters:
locate.FindPlayers returns inert snapshots alongside the live player, and a
write to a snapshot changes a number nothing reads. locate.ResolveLocalPlayer
is what answers that.

Ported from terrariabonker/player.py (spec 051, step 4).
*/
package player

import "github.com/ushineko/terrariabonker/internal/layout"

// Mem is the memory a player is read from and written to.
type Mem interface {
	ReadI32(addr uint32) (int32, bool)
	WriteI32(addr uint32, value int32) bool
}

// Player is one player copy, addressed by where its statLife field is.
type Player struct {
	Mem  Mem
	Life uint32
}

// New is a handle on the player whose statLife is at life.
func New(mem Mem, life uint32) *Player { return &Player{Mem: mem, Life: life} }

// at is the address of a field, which may be in front of statLife.
func (p *Player) at(off int) uint32 {
	return uint32(int(p.Life) + off) //nolint:gosec // a 32-bit address, deliberately
}

// field is one int32, and whether it was readable. Unreadable means the object
// has moved or the process has gone, which the caller has to tell apart from a
// legitimate zero.
func (p *Player) field(off int) (int32, bool) { return p.Mem.ReadI32(p.at(off)) }

// StatLife is current life.
func (p *Player) StatLife() (int32, bool) { return p.field(layout.StatLifeOff) }

// StatLifeMax is the cap in effect, which is the permanent one plus whatever
// temporary bonuses are running.
func (p *Player) StatLifeMax() (int32, bool) { return p.field(layout.StatLifeMaxOff) }

// StatLifeMax2 is the permanent cap, which is what the save file stores.
func (p *Player) StatLifeMax2() (int32, bool) { return p.field(layout.StatLifeMax2Off) }

// StatMana is current mana.
func (p *Player) StatMana() (int32, bool) { return p.field(layout.StatManaOff) }

// StatManaMax is the mana cap in effect.
func (p *Player) StatManaMax() (int32, bool) { return p.field(layout.StatManaMaxOff) }

// StatManaMax2 is the permanent mana cap.
func (p *Player) StatManaMax2() (int32, bool) { return p.field(layout.StatManaMax2Off) }

// SetLife writes current life.
func (p *Player) SetLife(value int32) bool {
	return p.Mem.WriteI32(p.at(layout.StatLifeOff), value)
}

// SetMana writes current mana.
func (p *Player) SetMana(value int32) bool {
	return p.Mem.WriteI32(p.at(layout.StatManaOff), value)
}

/*
SetMaxLife raises the life cap.

Both fields are written. The game recomputes the cap in effect from the
permanent one plus temporary bonuses every frame, so the permanent one is what
persists and the other is only so the change shows before the next frame -- and
the game is usually paused when this runs, so without it nothing appears to
happen at all.
*/
func (p *Player) SetMaxLife(value int32) bool {
	ok := p.Mem.WriteI32(p.at(layout.StatLifeMax2Off), value)
	return p.Mem.WriteI32(p.at(layout.StatLifeMaxOff), value) && ok
}

// SetMaxMana raises the mana cap, both fields, for the reason above.
func (p *Player) SetMaxMana(value int32) bool {
	ok := p.Mem.WriteI32(p.at(layout.StatManaMax2Off), value)
	return p.Mem.WriteI32(p.at(layout.StatManaMaxOff), value) && ok
}

// HealFull sets life to the cap in effect, and reports false when the cap could
// not be read rather than writing a number it guessed.
func (p *Player) HealFull() bool {
	limit, ok := p.StatLifeMax()
	return ok && p.SetLife(limit)
}

// ManaFull sets mana to the cap in effect.
func (p *Player) ManaFull() bool {
	limit, ok := p.StatManaMax()
	return ok && p.SetMana(limit)
}
