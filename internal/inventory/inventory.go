/*
Package inventory is the inventory of one player copy.

Addressed by that copy's statLife, like everything else about a player, and the
Item[] pointer is re-read on every access rather than kept: the managed heap
moves the array, and a pointer held from a second ago addresses whatever is
there now. Reading it again each time is a syscall, and being wrong here writes
into another object.

Slot indices are positions in that array, 0..InventorySlots. The first ten are
the hotbar.

Ported from terrariabonker/inventory.py (spec 051, step 4).
*/
package inventory

import (
	"encoding/binary"
	"math"

	"github.com/ushineko/terrariabonker/internal/layout"
)

// Mem is the memory an inventory is read from and written to.
type Mem interface {
	Read(addr uint32, size int) []byte
	Write(addr uint32, data []byte) bool
	ReadU32(addr uint32) (uint32, bool)
	ReadI32(addr uint32) (int32, bool)
	WriteI32(addr uint32, value int32) bool
	WriteF32(addr uint32, value float32) bool
}

// Flags are an item's damage class and whether it is an accessory. They decide
// which modifiers the editor offers, so an item that is none of these is not
// simply unlabelled: it is one nothing can be reforged onto.
type Flags struct {
	Accessory bool `json:"accessory"`
	Melee     bool `json:"melee"`
	Magic     bool `json:"magic"`
	Ranged    bool `json:"ranged"`
	Summon    bool `json:"summon"`
}

// Slot is one inventory position and the item in it.
type Slot struct {
	Index     int    `json:"index"`
	ItemAddr  uint32 `json:"item_addr"`
	Type      int32  `json:"type"`
	Stack     int32  `json:"stack"`
	UseTime   int32  `json:"use_time"`
	UseAnim   int32  `json:"use_anim"`
	Pick      int32  `json:"pick"`
	TileBoost int32  `json:"tile_boost"`
	Damage    int32  `json:"damage"`
	AutoReuse int32  `json:"auto_reuse"`
	Rare      int32  `json:"rare"`
	Defense   int32  `json:"defense"`
	Prefix    int32  `json:"prefix"`
	Flags     Flags  `json:"flags"`
}

// Empty reports whether the slot holds nothing. Type zero is the game's own way
// of saying so; the Item object still exists.
func (s Slot) Empty() bool { return s.Type == 0 }

// IsPickaxe reports whether this is a pickaxe, which is any item with pick
// power above zero rather than a list of item types.
func (s Slot) IsPickaxe() bool { return s.Pick > 0 }

// Inventory is one player copy's inventory.
type Inventory struct {
	Mem  Mem
	Life uint32
}

// New is the inventory of the player whose statLife is at life.
func New(mem Mem, life uint32) *Inventory { return &Inventory{Mem: mem, Life: life} }

// at is the address of a player field, which is usually in front of statLife.
func (inv *Inventory) at(off int) uint32 {
	return uint32(int(inv.Life) + off) //nolint:gosec // a 32-bit address, deliberately
}

// ArrayAddr is where the Item[] is now. Re-read every time so it self-corrects
// when the heap moves it.
func (inv *Inventory) ArrayAddr() (uint32, bool) {
	ptr, ok := inv.Mem.ReadU32(inv.at(layout.InventoryPtrOff))
	return ptr, ok && ptr != 0
}

// itemAddr is the Item object in a slot, or nothing when the array has gone or
// the slot is genuinely empty of an object.
func (inv *Inventory) itemAddr(index int) (uint32, bool) {
	arr, ok := inv.ArrayAddr()
	if !ok {
		return 0, false
	}
	addr, ok := inv.Mem.ReadU32(arr + layout.ArrDataOff + uint32(index)*4) //nolint:gosec // a slot index
	return addr, ok && addr != 0
}

// ReadSlot is the item in a slot, or nothing when there is no object there.
func (inv *Inventory) ReadSlot(index int) (Slot, bool) {
	addr, ok := inv.itemAddr(index)
	if !ok {
		return Slot{}, false
	}
	return Slot{
		Index:     index,
		ItemAddr:  addr,
		Type:      inv.i32(addr, layout.ItemType),
		Stack:     inv.i32(addr, layout.ItemStack),
		UseTime:   inv.i32(addr, layout.ItemUseTime),
		UseAnim:   inv.i32(addr, layout.ItemUseAnim),
		Pick:      inv.i32(addr, layout.ItemPick),
		TileBoost: inv.i32(addr, layout.ItemTileBoost),
		Damage:    inv.i32(addr, layout.ItemDamage),
		AutoReuse: int32(inv.u8(addr, layout.ItemAutoReuse)),
		Rare:      inv.i32(addr, layout.ItemRare),
		Defense:   inv.i32(addr, layout.ItemDefense),
		Prefix:    int32(inv.u8(addr, layout.ItemPrefix)),
		Flags: Flags{
			Accessory: inv.u8(addr, layout.ItemAccessory) != 0,
			Melee:     inv.u8(addr, layout.ItemMelee) != 0,
			Magic:     inv.u8(addr, layout.ItemMagic) != 0,
			Ranged:    inv.u8(addr, layout.ItemRanged) != 0,
			Summon:    inv.u8(addr, layout.ItemSummon) != 0,
		},
	}, true
}

// i32 is one of an item's integer fields. An unreadable field reads as zero,
// which is what the Python's None becomes wherever a Slot is built: the object
// address was already checked, so a field that will not read means the object
// has just moved and the whole Slot is stale, not that this one number is.
func (inv *Inventory) i32(addr uint32, off int) int32 {
	v, _ := inv.Mem.ReadI32(addr + uint32(off)) //nolint:gosec // a field offset
	return v
}

// u8 is one of an item's byte fields.
func (inv *Inventory) u8(addr uint32, off int) byte {
	b := inv.Mem.Read(addr+uint32(off), 1) //nolint:gosec // a field offset
	if len(b) < 1 {
		return 0
	}
	return b[0]
}

// Slots is every slot that holds an item object, which is normally all of them.
func (inv *Inventory) Slots() []Slot {
	out := make([]Slot, 0, layout.InventorySlots)
	for i := range layout.InventorySlots {
		if slot, ok := inv.ReadSlot(i); ok {
			out = append(out, slot)
		}
	}
	return out
}

// FindType is the slots holding a given ItemID.
func (inv *Inventory) FindType(itemType int32) []int {
	out := []int{}
	for _, slot := range inv.Slots() {
		if slot.Type == itemType {
			out = append(out, slot.Index)
		}
	}
	return out
}

/*
The span of an item that the passive-potion check reads.

The four fields it cares about are between favorited and buffType, so one read
per item covers all of them. Four reads would be four syscalls per slot, and
this runs on a timer several times a second.
*/
const (
	potionLo = layout.ItemFavorited
	potionHi = layout.ItemBuffType + 4
)

// Potion is a favorited consumable and the buff it grants.
type Potion struct {
	Slot int   `json:"slot"`
	Buff int32 `json:"buff"`
}

/*
FavoritedPotions is each favorited consumable carrying a buff.

The favorite is the player's opt-in. Without it every potion picked up would
start doing something. Consumable is what keeps pets, light pets and mounts out:
they carry a buffType too, and summoning a pet because it was in the bag is not
what anyone means by a potion.
*/
func (inv *Inventory) FavoritedPotions(minStack int32) []Potion {
	out := []Potion{}
	arr, ok := inv.ArrayAddr()
	if !ok {
		return out
	}
	for i := range layout.InventorySlots {
		addr, ok := inv.Mem.ReadU32(arr + layout.ArrDataOff + uint32(i)*4) //nolint:gosec // a slot index
		if !ok || addr == 0 {
			continue
		}
		w := inv.Mem.Read(addr+potionLo, potionHi-potionLo)
		if len(w) < potionHi-potionLo {
			continue
		}
		// The two byte gates first, because they are the cheap ones.
		if w[layout.ItemFavorited-potionLo] == 0 || w[layout.ItemConsumable-potionLo] == 0 {
			continue
		}
		if word(w, layout.ItemStack-potionLo) < minStack {
			continue
		}
		if buff := word(w, layout.ItemBuffType-potionLo); buff > 0 {
			out = append(out, Potion{Slot: i, Buff: buff})
		}
	}
	return out
}

// word is a signed word inside an already-read span.
func word(buf []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(buf[off:])) //nolint:gosec // a word, as its bits
}

// Rod is a fishing rod and its power.
type Rod struct {
	Slot  int  `json:"slot"`
	Power byte `json:"power"`
}

// Bait is a bait stack, its power and how much of it is left.
type Bait struct {
	Slot  int   `json:"slot"`
	Power byte  `json:"power"`
	Stack int32 `json:"stack"`
}

// Gear is the fishing tackle a player is carrying.
type Gear struct {
	Rods  []Rod  `json:"rods"`
	Baits []Bait `json:"baits"`
}

/*
FishingGear is every rod and every bait stack, not the first of each.

A player may carry several rods, and topping up one bait stack while another
runs dry is not "bait never runs out".
*/
func (inv *Inventory) FishingGear() Gear {
	out := Gear{Rods: []Rod{}, Baits: []Bait{}}
	arr, ok := inv.ArrayAddr()
	if !ok {
		return out
	}
	for i := range layout.InventorySlots {
		addr, ok := inv.Mem.ReadU32(arr + layout.ArrDataOff + uint32(i)*4) //nolint:gosec // a slot index
		if !ok || addr == 0 {
			continue
		}
		w := inv.Mem.Read(addr+layout.ItemFishingPole, 8)
		if len(w) < 8 {
			continue
		}
		if inv.i32(addr, layout.ItemType) == 0 {
			continue
		}
		pole, bait := w[0], w[layout.ItemBait-layout.ItemFishingPole]
		if pole != 0 {
			out.Rods = append(out.Rods, Rod{Slot: i, Power: pole})
		}
		if bait != 0 {
			out.Baits = append(out.Baits, Bait{Slot: i, Power: bait, Stack: inv.i32(addr, layout.ItemStack)})
		}
	}
	return out
}

// SelectedSlot is the hotbar slot the player is holding, and reports false when
// it reads as something that is not one.
func (inv *Inventory) SelectedSlot() (int, bool) {
	v, ok := inv.Mem.ReadI32(inv.at(layout.SelectedItemOff))
	if !ok || v < 0 || v > 9 {
		return 0, false
	}
	return int(v), true
}

/*
HoldingRod reports whether a fishing rod is in the player's hand right now.

Auto-catch needs this before it presses anything at empty water: the use button
is not fishing-specific, so "cast again" against a sword is "swing your sword",
which is what it did when it could see the water and not the hand.
*/
func (inv *Inventory) HoldingRod() bool {
	slot, ok := inv.SelectedSlot()
	if !ok {
		return false
	}
	for _, rod := range inv.FishingGear().Rods {
		if rod.Slot == slot {
			return true
		}
	}
	return false
}

/*
NonemptyCount is how many slots hold a real item.

It tells the live player from a load-time snapshot: the live one's inventory
reflects actual play while a snapshot holds only the starting items. Used when
activity sampling cannot decide, which is whenever the game is paused.
*/
func (inv *Inventory) NonemptyCount() int {
	n := 0
	for _, slot := range inv.Slots() {
		if !slot.Empty() {
			n++
		}
	}
	return n
}

// setI32 writes one of an item's integer fields.
func (inv *Inventory) setI32(index, off int, value int32) bool {
	addr, ok := inv.itemAddr(index)
	if !ok {
		return false
	}
	return inv.Mem.WriteI32(addr+uint32(off), value) //nolint:gosec // a field offset
}

// setByte writes one of an item's byte fields.
func (inv *Inventory) setByte(index, off int, value byte) bool {
	addr, ok := inv.itemAddr(index)
	if !ok {
		return false
	}
	return inv.Mem.Write(addr+uint32(off), []byte{value}) //nolint:gosec // a field offset
}

// SetStack writes how many of the item are in the slot.
func (inv *Inventory) SetStack(index int, value int32) bool {
	return inv.setI32(index, layout.ItemStack, value)
}

// SetType writes which item is in the slot.
func (inv *Inventory) SetType(index int, value int32) bool {
	return inv.setI32(index, layout.ItemType, value)
}

// SetDamage writes the item's damage.
func (inv *Inventory) SetDamage(index int, value int32) bool {
	return inv.setI32(index, layout.ItemDamage, value)
}

// SetPick writes the item's pickaxe power.
func (inv *Inventory) SetPick(index int, value int32) bool {
	return inv.setI32(index, layout.ItemPick, value)
}

// SetDefense writes the defense the item grants.
func (inv *Inventory) SetDefense(index int, value int32) bool {
	return inv.setI32(index, layout.ItemDefense, value)
}

// SetTileBoost writes the item's extra placement reach, in tiles.
func (inv *Inventory) SetTileBoost(index int, value int32) bool {
	return inv.setI32(index, layout.ItemTileBoost, value)
}

// SetAutoReuse turns auto-swing on or off for the item in this slot.
func (inv *Inventory) SetAutoReuse(index int, on bool) bool {
	var v byte
	if on {
		v = 1
	}
	return inv.setByte(index, layout.ItemAutoReuse, v)
}

/*
SetPrefix writes the item's modifier tier.

The byte alone is only the name. The bonuses live in the item's own fields --
see ApplyPrefixStats, which the service calls with the item's base stats.
*/
func (inv *Inventory) SetPrefix(index int, value int32) bool {
	return inv.setByte(index, layout.ItemPrefix, byte(value&0xFF)) //nolint:gosec // a byte field
}

/*
SetFishingPower writes a rod's fishing power.

A byte field, so a value that does not fit one is refused rather than wrapped
into a rod that fishes worse than it did.
*/
func (inv *Inventory) SetFishingPower(index int, value int32) bool {
	if value < 0 || value > 255 {
		return false
	}
	return inv.setByte(index, layout.ItemFishingPole, byte(value)) //nolint:gosec // range-checked above
}

/*
SetUseSpeed sets how fast the item swings, mines or places. Lower is faster.

useAnim is the visual and useTime is the effect, and they are equal on most
items and not on all, so both are written.
*/
func (inv *Inventory) SetUseSpeed(index int, useTime, useAnim int32) bool {
	addr, ok := inv.itemAddr(index)
	if !ok {
		return false
	}
	written := inv.Mem.WriteI32(addr+layout.ItemUseTime, useTime)
	return inv.Mem.WriteI32(addr+layout.ItemUseAnim, useAnim) && written
}

/*
LongReach gives every non-empty item extended placement reach, and is the slots
it touched.

tileBoost only affects placing tiles, so this is harmless on an item that places
none and is applied inventory-wide rather than to the held slot alone.
*/
func (inv *Inventory) LongReach(tiles int32) []int {
	hit := []int{}
	for _, slot := range inv.Slots() {
		if !slot.Empty() {
			inv.SetTileBoost(slot.Index, tiles)
			hit = append(hit, slot.Index)
		}
	}
	return hit
}

/*
MakeFastMining speeds up every pickaxe, and is the slots it touched.

A pickaxe is any item with pick power above zero. Pass a pick of -1 to leave the
power alone and change only the speed.
*/
func (inv *Inventory) MakeFastMining(useTime, useAnim, pick int32) []int {
	hit := []int{}
	for _, slot := range inv.Slots() {
		if !slot.IsPickaxe() {
			continue
		}
		inv.SetUseSpeed(slot.Index, useTime, useAnim)
		if pick >= 0 {
			inv.SetPick(slot.Index, pick)
		}
		hit = append(hit, slot.Index)
	}
	return hit
}

/*
prefixField is one field a modifier scales, and where its base value comes from.

One multiplier can drive several fields -- in the game usetime scales
useAnimation, useTime and reuseDelay -- and each scales from *its own* base,
which is why the base key is carried per field and not per stat. useAnimation
and useTime are equal on most weapons and not on all, so sharing one base would
quietly rewrite one of them to the other's value. reuseDelay is absent because
its offset is unknown.
*/
type prefixField struct {
	key   string
	off   int
	float bool
}

// prefixFields is each modifier stat and the fields it scales. The order is the
// Python's, which is the order the writes happen in.
var prefixFields = []struct {
	stat   string
	fields []prefixField
}{
	{"damage", []prefixField{{"damage", layout.ItemDamage, false}}},
	{"knockback", []prefixField{{"knockback", layout.ItemKnockback, true}}},
	{"usetime", []prefixField{
		{"useanim", layout.ItemUseAnim, false},
		{"usetime", layout.ItemUseTime, false},
	}},
	{"scale", []prefixField{{"scale", layout.ItemScale, true}}},
	{"shootspeed", []prefixField{{"shootspeed", layout.ItemShootSpeed, true}}},
	{"mana", []prefixField{{"mana", layout.ItemMana, false}}},
	{"crit", []prefixField{{"crit", layout.ItemCrit, false}}},
}

// additive is the bonuses the game adds to a field rather than multiplying into
// it.
var additive = map[string]bool{"crit": true}

// PrefixResult is what a modifier wrote, and which of its bonuses had nowhere
// to go.
type PrefixResult struct {
	Written map[string]float64 `json:"written"`
	Skipped []string           `json:"skipped"`
}

/*
ApplyPrefixStats scales an item's fields by a modifier's multipliers, from base.

base is the field values of a *pristine* item of this type, so applying the same
modifier twice gives the same answer and switching modifiers cannot compound.
Scaling the item's current values instead would drift a little further every
time.

Every scaled field is written, not only the ones this modifier changes. A
modifier the game applies always lands on a freshly reset item, so switching
from Godly to Large has to put damage back to base rather than leave Godly's
behind, and clearing the modifier has to restore everything. A field this
modifier does not touch gets a multiplier of one.

Integer fields are rounded half to even, which is what .NET's Math.Round does.

A bonus with no verified offset is named in the result rather than dropped: a
modifier that quietly loses its crit bonus is the bug this reporting exists for.
*/
func (inv *Inventory) ApplyPrefixStats(index int, mults, base map[string]float64) PrefixResult {
	addr, ok := inv.itemAddr(index)
	if !ok {
		return PrefixResult{Written: map[string]float64{}, Skipped: sortedKeys(mults)}
	}
	written := map[string]float64{}
	skipped := map[string]bool{}
	for stat := range mults {
		if !knownStat(stat) {
			skipped[stat] = true
		}
	}
	for _, entry := range prefixFields {
		add := additive[entry.stat]
		neutral := 1.0
		if add {
			neutral = 0.0
		}
		amount, given := mults[entry.stat]
		if !given {
			amount = neutral
		}
		for _, field := range entry.fields {
			from, known := base[field.key]
			if !known {
				skipped[field.key] = true
				continue
			}
			value := from * amount
			if add {
				value = from + amount
			}
			if field.float {
				inv.Mem.WriteF32(addr+uint32(field.off), float32(value)) //nolint:gosec // a field offset
			} else {
				inv.Mem.WriteI32(addr+uint32(field.off), int32(math.RoundToEven(value))) //nolint:gosec // a field offset
			}
			if amount != neutral {
				written[field.key] = value
			}
		}
	}
	return PrefixResult{Written: written, Skipped: sortedSet(skipped)}
}

// knownStat reports whether a modifier stat has a field to write.
func knownStat(stat string) bool {
	for _, entry := range prefixFields {
		if entry.stat == stat {
			return true
		}
	}
	return false
}
