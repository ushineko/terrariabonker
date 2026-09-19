/*
Package service is the layer every caller goes through: find the game, find the
player, read them, write to them.

It exists because the rules about *which copy* to touch are not obvious and are
silent when broken. The game keeps more than one object that looks like a
player: the live one and one or two load-time snapshots. Writes go to all of
them, so the live one is always hit and the inert ones ignore what lands on
them. Reads come from the live one alone, because a snapshot holds whatever the
slot contained when it was taken and reporting that is reporting fiction.

Locating is nearly all of a read's cost, a full scan of the heap per call, so a
long-lived caller keeps the result and re-validates it cheaply. A one-shot run
locates once and exits.

Ported from terrariabonker/service.py (spec 051, step 5). The build gate, the
world and tile operations and everything built on the patcher arrive with their
own modules in the later steps.
*/
package service

import (
	"errors"
	"time"

	"github.com/ushineko/terrariabonker/internal/inventory"
	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/player"
	"github.com/ushineko/terrariabonker/internal/proc"
)

// Mem is everything the service reads and writes through. It is proc.Mem in
// production and a planted buffer in a test.
type Mem interface {
	locate.ExecMem
	inventory.Mem
	player.Mem
	// ExePath is where the mapped game came from, which is how the build gate
	// finds both the version and Steam's manifest.
	ExePath() string
}

/*
Error is a failure with something to say to the user: the game is not running,
no player is loaded, the build is not one this knows.

Separate from the errors underneath it because those are about a syscall and
these are about what to do next.
*/
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// ErrNoPlayer is what every read reports when nothing is loaded. It is a
// distinct value because the window shows it as a normal state rather than as
// something going wrong.
var ErrNoPlayer = &Error{Message: "no player found. Load into a world first."}

/*
How long the fallback guess at the live copy is allowed to sample for.

Only reached when the resolver cannot answer. The window is short because it is
spent while the caller waits, and it is nearly always wasted: the game is paused
whenever the trainer has focus, so nothing moves to be seen moving.
*/
const (
	liveSamples = 6
	liveGap     = 80 * time.Millisecond
)

// Service is bound to one running game.
type Service struct {
	Mem Mem
	PID int

	blocks []locate.Block // the located player copies, kept and re-validated
	anchor uint32         // where get_LocalPlayer was found
	found  bool           // and whether it was
	build  *Build         // the running build, once it reads as a real one

	// Main's static block, found once. Finding it is a full memory scan, and
	// the statics do not move while the process lives.
	mainBase uint32
	haveBase bool
}

// New is a service over memory that is already open.
func New(mem Mem, pid int) *Service { return &Service{Mem: mem, PID: pid} }

// Connect attaches to the running game. The caller is assumed to have root
// already: this does not acquire it, and asking for it is the CLI's business.
func Connect() (*Service, error) {
	pid, err := proc.FindPID()
	if err != nil {
		return nil, &Error{Message: err.Error()}
	}
	return New(proc.New(pid), pid), nil
}

// Invalidate drops what was located, so the next call scans from scratch.
func (s *Service) Invalidate() {
	s.blocks, s.anchor, s.found, s.build = nil, 0, false, nil
	s.mainBase, s.haveBase = 0, false
}

/*
Players is every player copy, located once and re-validated after that.

The scan is the expensive thing here, so it is not repeated while the cached
addresses still hold up.
*/
func (s *Service) Players() ([]locate.Block, error) {
	if s.blocks != nil && s.blocksValid(s.blocks) {
		return s.blocks, nil
	}
	blocks := locate.FindPlayers(s.Mem)
	if len(blocks) == 0 {
		s.blocks = nil
		return nil, ErrNoPlayer
	}
	s.blocks = blocks
	return blocks, nil
}

/*
blocksValid re-checks cached addresses with a few reads instead of a scan.

Two things have to hold. Every cached address still reads back as the same named
player, and the live player is still one of them -- the second catches a world
reload that moved the player to a new object, which would otherwise leave every
write landing on dead copies.

No ground truth means the cache cannot be confirmed, because a dead copy can
still read back as the same named player. That is a rescan, not a shrug: writing
to a corpse is silent.
*/
func (s *Service) blocksValid(blocks []locate.Block) bool {
	for _, b := range blocks {
		fresh, ok := locate.ReadBlock(s.Mem, b.LifeAddr)
		if !ok || fresh.Name != b.Name {
			return false
		}
	}
	live, ok := s.resolveLive()
	if !ok {
		return false
	}
	for _, b := range blocks {
		if b.LifeAddr == live.LifeAddr {
			return true
		}
	}
	return false
}

// resolveLive is ground truth through a kept anchor, re-finding the anchor when
// it stops resolving.
func (s *Service) resolveLive() (locate.Block, bool) {
	if s.found {
		if blk, ok := locate.LocalPlayerAt(s.Mem, s.anchor); ok {
			return blk, true
		}
	}
	s.anchor, s.found = locate.FindLocalPlayerAnchor(s.Mem)
	if !s.found {
		return locate.Block{}, false
	}
	return locate.LocalPlayerAt(s.Mem, s.anchor)
}

/*
selectLive picks the live copy.

Ground truth first, which works while the game is paused and so works nearly
always. The activity guess is the fallback, and the richest inventory is the
fallback to that -- unreliable, because a frozen snapshot can hold more items
than the live player, which is why it is last and why the resolver exists.
*/
func (s *Service) selectLive(blocks []locate.Block) locate.Block {
	if live, ok := s.resolveLive(); ok {
		return live
	}
	if live, ok := locate.PickLive(s.Mem, blocks, liveSamples, liveGap); ok {
		return live
	}
	best, most := blocks[0], -1
	for _, b := range blocks {
		if n := inventory.New(s.Mem, b.LifeAddr).NonemptyCount(); n > most {
			best, most = b, n
		}
	}
	return best
}

// LiveBlock is the live player copy.
func (s *Service) LiveBlock() (locate.Block, error) {
	blocks, err := s.Players()
	if err != nil {
		return locate.Block{}, err
	}
	return s.selectLive(blocks), nil
}

// allTargets is every copy as a handle to write through.
func (s *Service) allTargets() ([]*player.Player, error) {
	blocks, err := s.Players()
	if err != nil {
		return nil, err
	}
	out := make([]*player.Player, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, player.New(s.Mem, b.LifeAddr))
	}
	return out, nil
}

// liveInventory is the inventory to read from.
func (s *Service) liveInventory() (*inventory.Inventory, error) {
	live, err := s.LiveBlock()
	if err != nil {
		return nil, err
	}
	return inventory.New(s.Mem, live.LifeAddr), nil
}

/*
allInventories is every copy's inventory, *for writing*.

Writes go to all of them so the live copy is always hit. Do not read from these
to decide anything: the copies are not identical, a snapshot holds whatever the
slot contained when it was taken, and the first is not necessarily the live one.
Read from liveInventory, which is what Inventory reports.
*/
func (s *Service) allInventories() ([]*inventory.Inventory, error) {
	blocks, err := s.Players()
	if err != nil {
		return nil, err
	}
	out := make([]*inventory.Inventory, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, inventory.New(s.Mem, b.LifeAddr))
	}
	return out, nil
}

// PlayerState is a player as the window shows them.
type PlayerState struct {
	Name    string `json:"name"`
	HP      int32  `json:"hp"`
	MaxHP   int32  `json:"max_hp"`
	Mana    int32  `json:"mana"`
	MaxMana int32  `json:"max_mana"`
}

// ItemSlot is one inventory slot as the window shows it.
type ItemSlot struct {
	Slot      int             `json:"slot"`
	Type      int32           `json:"type"`
	Stack     int32           `json:"stack"`
	Damage    int32           `json:"damage"`
	AutoReuse int32           `json:"auto_reuse"`
	UseTime   int32           `json:"use_time"`
	Pick      int32           `json:"pick"`
	TileBoost int32           `json:"tile_boost"`
	UseAnim   int32           `json:"use_anim"`
	Rare      int32           `json:"rare"`
	Defense   int32           `json:"defense"`
	Prefix    int32           `json:"prefix"`
	Flags     inventory.Flags `json:"flags"`
}

// Snapshot is one read of everything the window puts on screen.
type Snapshot struct {
	PID       int `json:"pid"`
	Build     `json:",inline"`
	Copies    int          `json:"copies"`
	Player    *PlayerState `json:"player"`
	Inventory []ItemSlot   `json:"inventory"`
}

/*
Snapshot reads the live player and, unless told not to, their inventory.

No player loaded is not a failure here. The window asks for this on a timer and
shows "no player" as an ordinary state, so an empty snapshot comes back rather
than an error to render.
*/
func (s *Service) Snapshot(withInventory bool) Snapshot {
	out := Snapshot{PID: s.PID, Build: s.BuildInfo(), Inventory: []ItemSlot{}}
	blocks, err := s.Players()
	if err != nil {
		return out
	}
	out.Copies = len(blocks)
	live := s.selectLive(blocks)
	out.Player = &PlayerState{
		Name: live.Name, HP: live.StatLife, MaxHP: live.StatLifeMax,
		Mana: live.StatMana, MaxMana: live.StatManaMax,
	}
	if withInventory {
		out.Inventory = toSlots(inventory.New(s.Mem, live.LifeAddr).Slots())
	}
	return out
}

// Inventory is the live player's slots.
func (s *Service) Inventory() ([]ItemSlot, error) {
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	return toSlots(inv.Slots()), nil
}

// toSlots is the inventory package's slots as the window's.
func toSlots(slots []inventory.Slot) []ItemSlot {
	out := make([]ItemSlot, 0, len(slots))
	for _, s := range slots {
		out = append(out, ItemSlot{
			Slot: s.Index, Type: s.Type, Stack: s.Stack, Damage: s.Damage,
			AutoReuse: s.AutoReuse, UseTime: s.UseTime, Pick: s.Pick,
			TileBoost: s.TileBoost, UseAnim: s.UseAnim, Rare: s.Rare,
			Defense: s.Defense, Prefix: s.Prefix, Flags: s.Flags,
		})
	}
	return out
}

/*
SetHP writes current life to every copy.

Every copy, because which one is live is a guess whenever the resolver cannot
answer, and a write to an inert copy costs nothing while a miss costs the whole
operation.
*/
func (s *Service) SetHP(value int32) error {
	return s.eachPlayer(func(p *player.Player) { p.SetLife(value) })
}

// SetHPMax fills each copy's life to that copy's own cap.
func (s *Service) SetHPMax() error {
	return s.eachPlayer(func(p *player.Player) { p.HealFull() })
}

// SetMana writes current mana to every copy.
func (s *Service) SetMana(value int32) error {
	return s.eachPlayer(func(p *player.Player) { p.SetMana(value) })
}

// SetManaMax fills each copy's mana to that copy's own cap.
func (s *Service) SetManaMax() error {
	return s.eachPlayer(func(p *player.Player) { p.ManaFull() })
}

// SetMaxHP raises the life cap on every copy.
func (s *Service) SetMaxHP(value int32) error {
	return s.eachPlayer(func(p *player.Player) { p.SetMaxLife(value) })
}

// SetMaxMana raises the mana cap on every copy.
func (s *Service) SetMaxMana(value int32) error {
	return s.eachPlayer(func(p *player.Player) { p.SetMaxMana(value) })
}

// eachPlayer runs a write against every copy.
func (s *Service) eachPlayer(write func(*player.Player)) error {
	targets, err := s.allTargets()
	if err != nil {
		return err
	}
	for _, p := range targets {
		write(p)
	}
	return nil
}

// SetStack writes a slot's stack size in every copy.
func (s *Service) SetStack(slot int, value int32) error {
	return s.eachInventory(func(inv *inventory.Inventory) { inv.SetStack(slot, value) })
}

// eachInventory runs a write against every copy's inventory.
func (s *Service) eachInventory(write func(*inventory.Inventory)) error {
	invs, err := s.allInventories()
	if err != nil {
		return err
	}
	for _, inv := range invs {
		write(inv)
	}
	return nil
}

/*
FastMining speeds up every pickaxe in every copy, and reports the *live* copy's
slots.

Writing to all of them is the rule. The report is not "whichever copy happened
to be last": that is usually an inert snapshot holding whatever it held when it
was taken, and the count goes straight to the user.
*/
func (s *Service) FastMining(useTime, useAnim, pick int32) ([]int, error) {
	return s.sweep(func(inv *inventory.Inventory) []int {
		return inv.MakeFastMining(useTime, useAnim, pick)
	})
}

// LongReach extends placement reach in every copy, and reports the live copy's
// slots for the reason above.
func (s *Service) LongReach(tiles int32) ([]int, error) {
	return s.sweep(func(inv *inventory.Inventory) []int { return inv.LongReach(tiles) })
}

// sweep runs an inventory-wide edit on the live copy first, keeps what it
// touched, then runs it everywhere.
func (s *Service) sweep(edit func(*inventory.Inventory) []int) ([]int, error) {
	live, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	hit := edit(live)
	invs, err := s.allInventories()
	if err != nil {
		return nil, err
	}
	for _, inv := range invs {
		edit(inv)
	}
	return hit, nil
}

// IsNoPlayer reports whether an error is the ordinary "nothing loaded" one,
// which a caller shows differently from a failure.
func IsNoPlayer(err error) bool { return errors.Is(err, error(ErrNoPlayer)) }
