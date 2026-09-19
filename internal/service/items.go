package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/inventory"
	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Changing what is in a slot, and where an item's real stats come from.

An item is not its type. Setting the type alone gives a name with nothing behind
it -- no damage, no use time, no rarity -- because those are assigned when the
game builds the item and are not stored anywhere the type can reach. So a change
of item copies the block out of the game's own pristine template.
*/

// GiveSlots is the range a given item may land in: the main grid, skipping the
// coin, ammo and equipment slots.
const GiveSlots = 50

/*
ownItemAddrs is the addresses of the player's own item objects.

These are the ones this program edits, so they are the likeliest to be mistaken
for a pristine template -- and the cheapest contaminant to remove, because their
addresses are already known.
*/
func (s *Service) ownItemAddrs() map[uint32]bool {
	out := map[uint32]bool{}
	invs, err := s.allInventories()
	if err != nil {
		return out
	}
	for _, inv := range invs {
		for _, slot := range inv.Slots() {
			out[slot.ItemAddr] = true
		}
	}
	return out
}

/*
ItemVTable is the shared vtable of item objects, read from any real item.

The vtable is the same in every copy, but an inert one can be empty where the
live one is not -- so the live copy is read first and the others are a fallback,
rather than giving up because the first copy happened to hold nothing.
*/
func (s *Service) ItemVTable() (uint32, bool) {
	live, err := s.liveInventory()
	if err != nil {
		return 0, false
	}
	invs, err := s.allInventories()
	if err != nil {
		return 0, false
	}
	for _, inv := range append([]*inventory.Inventory{live}, invs...) {
		for _, slot := range inv.Slots() {
			if slot.Empty() {
				continue
			}
			if vt, ok := s.Mem.ReadU32(slot.ItemAddr); ok && vt != 0 {
				return vt, true
			}
		}
	}
	return 0, false
}

/*
TemplateAddr is the address of the pristine template for an item type.

Split out from the block because a few fields a modifier scales sit *beyond* the
copied span, so they have to be read from the object rather than from the block.
Kept per type: this is a full region scan and it is the same answer every time
within a session.
*/
func (s *Service) TemplateAddr(itemType int32) (uint32, bool) {
	if found, known := s.templateAddrs[itemType]; known {
		return found, found != 0
	}
	addr, ok := s.scanForTemplate(itemType)
	if s.templateAddrs == nil {
		s.templateAddrs = map[int32]uint32{}
	}
	s.templateAddrs[itemType] = addr
	return addr, ok
}

// scanForTemplate looks for an item object of a type, anywhere in the heap.
func (s *Service) scanForTemplate(itemType int32) (uint32, bool) {
	vt, ok := s.ItemVTable()
	if !ok {
		return 0, false
	}
	want := uint32(itemType) //nolint:gosec // an item type, as its bits
	for _, region := range s.Mem.Regions() {
		buf := s.Mem.Read(region.Start, region.Size())
		for i := 0; i+4 <= len(buf); i += 4 {
			if binary.LittleEndian.Uint32(buf[i:]) != want {
				continue
			}
			off := i - layout.ItemType
			if off < 0 || off+4 > len(buf) {
				continue
			}
			if binary.LittleEndian.Uint32(buf[off:]) != vt {
				continue
			}
			return region.Start + uint32(off), true //nolint:gosec // an offset in a 32-bit region
		}
	}
	return 0, false
}

/*
TemplateBlock is the field bytes of a pristine item of a type.

The game keeps one for every type. Nothing comes back when there is none, and the
caller falls back to setting a bare type.
*/
func (s *Service) TemplateBlock(itemType int32) ([]byte, bool) {
	addr, ok := s.TemplateAddr(itemType)
	if !ok {
		return nil, false
	}
	block := s.Mem.Read(addr+layout.CopyLo, layout.CopyHi-layout.CopyLo)
	if len(block) != layout.CopyHi-layout.CopyLo {
		return nil, false
	}
	return block, true
}

/*
PrefixBaseStats is a pristine item's values for every field a modifier scales.

Read from the template, which is what makes applying a modifier idempotent: the
same modifier twice gives the same stats, and switching from one to another gives
what that one would have given on a fresh item. Scaling the item's *current*
values instead would compound on every re-apply.
*/
func (s *Service) PrefixBaseStats(itemType int32) map[string]float64 {
	out := map[string]float64{}
	block, ok := s.TemplateBlock(itemType)
	if !ok {
		return out
	}
	addr, haveAddr := s.TemplateAddr(itemType)

	for _, f := range inventory.PrefixBaseFields {
		if i := f.Off - layout.CopyLo; i >= 0 && i+4 <= len(block) {
			out[f.Key] = decode(block[i:], f.Float)
			continue
		}
		// Past the copied span: read it off the template object itself.
		if !haveAddr {
			continue
		}
		raw := s.Mem.Read(addr+uint32(f.Off), 4) //nolint:gosec // a field offset
		if len(raw) == 4 {
			out[f.Key] = decode(raw, f.Float)
		}
	}
	return out
}

// decode is one field, read as whichever kind it is.
func decode(raw []byte, isFloat bool) float64 {
	bits := binary.LittleEndian.Uint32(raw)
	if isFloat {
		return float64(math.Float32frombits(bits))
	}
	return float64(int32(bits)) //nolint:gosec // a field, as its bits
}

/*
ApplyPrefix gives the item the modifier *and* the bonuses that modifier confers.

The prefix byte is only the name the tooltip prints. The game multiplies the
bonuses into the item's own fields, which is why setting the byte alone produced
a Godly weapon with nothing Godly about it.

Accessory modifiers legitimately change nothing here: the game reads the byte
when the item is equipped, so those already worked.
*/
func (s *Service) ApplyPrefix(invs []*inventory.Inventory, slot int, itemType, prefix int32) (inventory.PrefixResult, error) {
	mods, err := game.ItemPrefixes()
	if err != nil {
		return inventory.PrefixResult{}, err
	}
	mults := mods.Stats(int(prefix))
	base := s.PrefixBaseStats(itemType)

	result := inventory.PrefixResult{Written: map[string]float64{}, Skipped: []string{}}
	if len(base) > 0 {
		/*
			Applied even for a modifier with no multipliers, and for clearing one:
			the scaled fields are reset to base either way, which is what makes
			switching modifiers and removing them behave like the game's own
			reforge.
		*/
		for _, inv := range invs {
			result = inv.ApplyPrefixStats(slot, mults, base)
		}
	}
	for _, inv := range invs {
		inv.SetPrefix(slot, prefix)
	}
	return result, nil
}

/*
placeItem sets a slot to an item type in every copy.

Through the template block when there is one, so the item has real stats, and
otherwise a bare type -- which is a name with nothing behind it, but better than
refusing.
*/
func (s *Service) placeItem(invs []*inventory.Inventory, slot int, itemType int32, block []byte) {
	for _, inv := range invs {
		addr, ok := inv.ItemAddr(slot)
		if ok && block != nil {
			s.Mem.Write(addr+layout.CopyLo, block)
			continue
		}
		inv.SetType(slot, itemType)
	}
}

// ItemEdit is what a caller wants a slot to become. A field left nil is left
// alone.
type ItemEdit struct {
	Stack     *int32
	Damage    *int32
	AutoReuse *bool
	UseTime   *int32
	UseAnim   *int32
	Pick      *int32
	TileBoost *int32
	Defense   *int32
	Prefix    *int32

	/*
		ExpectType is what the caller believed the slot held.

		Given, it is checked before anything is written: if the game moved items
		since the caller looked, writing would template their stale item over
		whatever is really there and destroy it.
	*/
	ExpectType *int32
}

// SetItem changes what is in a slot.
func (s *Service) SetItem(slot int, itemType int32, edit ItemEdit) error {
	invs, err := s.allInventories()
	if err != nil {
		return err
	}
	live, err := s.liveInventory()
	if err != nil {
		return err
	}
	/*
		Read from the *live* copy. Writes go to every copy because the inert ones
		ignore them, but a read has to come from the copy the caller was looking
		at: the inventory reported to them is the live one, so comparing against
		another compares against something they never saw. The copies are not
		identical -- a snapshot holds whatever was in the slot when it was taken,
		which is how editing the last hotbar slot came to be refused for holding
		a Green Torch while both the game and the grid showed a regular one.
	*/
	cur, haveCur := live.ReadSlot(slot)

	if edit.ExpectType != nil {
		names, err := game.ItemNames()
		if err != nil {
			return err
		}
		if !haveCur {
			return &Error{Message: fmt.Sprintf(
				"slot %d could not be read to verify it still holds %s -- refusing to write",
				slot, names.Label(int(*edit.ExpectType)))}
		}
		if cur.Type != *edit.ExpectType {
			return &Error{Message: fmt.Sprintf(
				"slot %d now holds %s, not %s -- it changed in-game. Refresh and try again.",
				slot, names.Label(int(cur.Type)), names.Label(int(*edit.ExpectType)))}
		}
	}

	/*
		Only re-template when the type actually changes: a field tweak on the same
		item must not wipe the edits, and must stay fast rather than paying for a
		scan.
	*/
	changed := !haveCur || itemType != cur.Type
	var block []byte
	// Type zero clears the slot, and there is no template for "empty".
	if changed && itemType != 0 {
		block, _ = s.TemplateBlock(itemType)
	}
	if changed {
		s.placeItem(invs, slot, itemType, block)
	}
	/*
		The modifier goes on *before* the explicit edits, so a damage the caller
		typed wins over the one the modifier computed. It is applied from the
		item's pristine stats rather than its current ones.
	*/
	if edit.Prefix != nil && itemType != 0 {
		if _, err := s.ApplyPrefix(invs, slot, itemType, *edit.Prefix); err != nil {
			return err
		}
	}
	for _, inv := range invs {
		applyEdit(inv, slot, edit)
	}
	return nil
}

// applyEdit writes the fields a caller named, and leaves the rest alone.
func applyEdit(inv *inventory.Inventory, slot int, edit ItemEdit) {
	if edit.Stack != nil {
		inv.SetStack(slot, *edit.Stack)
	}
	if edit.Damage != nil {
		inv.SetDamage(slot, *edit.Damage)
	}
	if edit.AutoReuse != nil {
		inv.SetAutoReuse(slot, *edit.AutoReuse)
	}
	if edit.UseTime != nil {
		anim := *edit.UseTime
		if edit.UseAnim != nil {
			anim = *edit.UseAnim
		}
		inv.SetUseSpeed(slot, *edit.UseTime, anim)
	}
	if edit.Pick != nil {
		inv.SetPick(slot, *edit.Pick)
	}
	if edit.TileBoost != nil {
		inv.SetTileBoost(slot, *edit.TileBoost)
	}
	if edit.Defense != nil {
		inv.SetDefense(slot, *edit.Defense)
	}
}

/*
GiveItem puts a fully-statted item in the first empty main slot, and is which
slot it used.

Which slot is free is read from the *live* copy: an inert snapshot shows whatever
was there when it was taken, so trusting it can pick a slot that is empty in the
snapshot and occupied in the game -- and the give would land on top of a real item
and destroy it.
*/
func (s *Service) GiveItem(itemType, stack int32) (int, error) {
	invs, err := s.allInventories()
	if err != nil {
		return 0, err
	}
	live, err := s.liveInventory()
	if err != nil {
		return 0, err
	}
	bySlot := map[int]inventory.Slot{}
	for _, slot := range live.Slots() {
		bySlot[slot.Index] = slot
	}
	empty := -1
	for i := range GiveSlots {
		if slot, there := bySlot[i]; there && slot.Empty() {
			empty = i
			break
		}
	}
	if empty < 0 {
		return 0, &Error{Message: "inventory full -- no empty slot to give into"}
	}
	block, _ := s.TemplateBlock(itemType)
	s.placeItem(invs, empty, itemType, block)
	for _, inv := range invs {
		inv.SetStack(empty, stack)
	}
	return empty, nil
}
