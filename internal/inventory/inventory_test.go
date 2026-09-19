package inventory_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/inventory"
	"github.com/ushineko/terrariabonker/internal/memtest"
)

/*
Reading an inventory, and writing to one.

An item's fields are two hundred and fifty bytes of adjacent integers, and a
reader that is one field out gets a plausible number from the wrong place --
useTime read as stack, defense read as headSlot. So the reads are checked
against what the fixture planted, and every write is checked as the whole
buffer: a field written to the wrong offset then fails as surely as one written
with the wrong value.

The buffer is compared by digest rather than by writing a hundred kilobytes of
hex into this file. A failure says the bytes changed and not which, which is
what the assertions beside it are for -- and a change to what a write does is
meant to be a decision rather than a surprise.

Every digest and every expectation here was agreed with the implementation this
was ported from, while both existed.
*/

// image is the whole planted buffer, as one short string.
func image(mem *memtest.FakeMem) string {
	sum := sha256.Sum256([]byte(mem.Hex()))
	return hex.EncodeToString(sum[:8])
}

/*
Every slot reads back as what was planted in it.

The image is described field by field in the fixture, so this is the round trip:
each named field written at its own offset, read back through the reader that
has to find it again.
*/
func TestReadingSlotsIsWhatWasPlanted(t *testing.T) {
	slots := inventory.New(plant(), life).Slots()

	byIndex := map[int]inventory.Slot{}
	for _, s := range slots {
		byIndex[s.Index] = s
	}
	require.NotContains(t, byIndex, 12, "a slot with no object was read as one")
	require.Contains(t, byIndex, 13, "an empty slot is still a slot")

	for _, it := range inventoryImage {
		if it.absent {
			continue
		}
		got, read := byIndex[it.slot]
		require.Truef(t, read, "slot %d was not read", it.slot)
		require.Equalf(t, itemAddr(it.slot), got.ItemAddr,
			"slot %d points somewhere else", it.slot)
		for _, f := range it.fields {
			want, surfaced := plantedField(got, f.name)
			if !surfaced {
				continue // a field a slot does not carry; the sweeps below cover those
			}
			require.Equalf(t, f.value, want,
				"slot %d read %s from the wrong place", it.slot, f.name)
		}
	}
}

/*
plantedField is one field of a read slot, under the name the fixture plants it
by, and whether the slot carries it at all.

A slot surfaces the numbers the window shows and the damage classes a modifier
is chosen by. The rest -- the float fields, the buff a potion grants, a rod's
power -- are read by the sweeps rather than by the slot, and are covered there.
*/
func plantedField(s inventory.Slot, name string) (float64, bool) {
	numbers := map[string]int32{
		"ITEM_TYPE": s.Type, "ITEM_STACK": s.Stack, "ITEM_PICK": s.Pick,
		"ITEM_USE_TIME": s.UseTime, "ITEM_USE_ANIM": s.UseAnim,
		"ITEM_DAMAGE": s.Damage, "ITEM_RARE": s.Rare,
		"ITEM_AUTOREUSE": s.AutoReuse, "ITEM_TILEBOOST": s.TileBoost,
		"ITEM_DEFENSE": s.Defense, "ITEM_PREFIX": s.Prefix,
	}
	if v, carried := numbers[name]; carried {
		return float64(v), true
	}
	flags := map[string]bool{
		"ITEM_ACCESSORY": s.Flags.Accessory, "ITEM_MELEE": s.Flags.Melee,
		"ITEM_MAGIC": s.Flags.Magic, "ITEM_RANGED": s.Flags.Ranged,
		"ITEM_SUMMON": s.Flags.Summon,
	}
	on, carried := flags[name]
	if !carried {
		return 0, false
	}
	if on {
		return 1, true
	}
	return 0, true
}

/*
The sweeps pick out exactly the slots the fixture says they should.

Each one is a rule about what an item is -- a potion is favorited and consumable
and has a buff, a rod has a fishing power and a type -- and the image carries a
near miss for every one of them.
*/
func TestTheSweepsFindWhatWasPlanted(t *testing.T) {
	inv := inventory.New(plant(), life)

	// Favorited, consumable, with a buff. The pet in slot 3 is favorited and
	// not consumable; the potion in slot 2 is consumable and not favorited.
	require.Equal(t, [][2]int32{{1, 11}, {4, 12}}, potions(inv, 1),
		"a different set of potions is favorited")
	// And a stack gate drops slot 4, which holds one.
	require.Equal(t, [][2]int32{{1, 11}}, potions(inv, 2), "the stack gate differs")

	gear := inv.FishingGear()
	require.Equal(t, []inventory.Rod{{Slot: 5, Power: 25}, {Slot: 7, Power: 40}}, gear.Rods,
		"a different set of rods; slot 8 has a power and no item")
	require.Equal(t, []inventory.Bait{{Slot: 6, Power: 15, Stack: 12}}, gear.Baits)

	slot, ok := inv.SelectedSlot()
	require.True(t, ok, "the held slot did not read")
	require.Equal(t, selectedSlot, slot)
	require.False(t, inv.HoldingRod(), "the held slot is the pickaxe, not the rod")

	require.Equal(t, 10, inv.NonemptyCount(), "a different number of items is carried")
	require.Equal(t, []int{0}, inv.FindType(3509), "the pickaxe is somewhere else")
	require.Empty(t, inv.FindType(999999), "a type nobody has was found")
}

// potions is the favorited potions as slot and buff pairs.
func potions(inv *inventory.Inventory, min int32) [][2]int32 {
	out := [][2]int32{}
	for _, p := range inv.FavoritedPotions(min) {
		out = append(out, [2]int32{int32(p.Slot), p.Buff}) //nolint:gosec // a slot index
	}
	return out
}

/*
Every write lands on the same bytes.

The digest covers the whole buffer, so a field written at the wrong offset fails
here even though the value it wrote was right -- which is the failure this file
exists for, because the fields are adjacent and a neighbour takes a plausible
number without complaint.
*/
func TestWritingItems(t *testing.T) {
	for _, c := range []struct {
		name   string
		digest string
		run    func(inv *inventory.Inventory) bool
	}{
		{"stack", "154a4c58a990a31c", func(i *inventory.Inventory) bool { return i.SetStack(1, 99) }},
		{"type", "350cd7b485f3a844", func(i *inventory.Inventory) bool { return i.SetType(13, 3507) }},
		{"damage", "38f3220c8e2b9bf3", func(i *inventory.Inventory) bool { return i.SetDamage(9, 500) }},
		{"pick", "396d2f9d89d6b3ad", func(i *inventory.Inventory) bool { return i.SetPick(0, 200) }},
		{"defense", "18bae4f707e12dec", func(i *inventory.Inventory) bool { return i.SetDefense(9, 12) }},
		{"tile boost", "afd5cc5fdc7d9be3", func(i *inventory.Inventory) bool { return i.SetTileBoost(0, 20) }},
		{"auto reuse on", "ea27509b66c3d0e7", func(i *inventory.Inventory) bool { return i.SetAutoReuse(9, true) }},
		{"auto reuse off", "7f75a2fe509d9b8d", func(i *inventory.Inventory) bool { return i.SetAutoReuse(0, false) }},
		{"prefix", "10e62d514fe07771", func(i *inventory.Inventory) bool { return i.SetPrefix(9, 81) }},
		// A prefix over a byte is masked rather than refused, because the game's
		// own field is a byte and the caller's number came from a list of them.
		{"prefix wraps", "227c7c80fa0581e5", func(i *inventory.Inventory) bool { return i.SetPrefix(9, 0x141) }},
		{"use speed", "8963d9d3e37ba8e1", func(i *inventory.Inventory) bool { return i.SetUseSpeed(0, 8, 13) }},
		{"fishing power", "db56a74ac3ae60c8", func(i *inventory.Inventory) bool { return i.SetFishingPower(5, 200) }},
		{"long reach", "68444e041632a664", func(i *inventory.Inventory) bool { return len(i.LongReach(20)) > 0 }},
		{"fast mining", "e139b346c5421fae", func(i *inventory.Inventory) bool { return len(i.MakeFastMining(8, 13, 200)) > 0 }},
		{"fast mining, power left alone", "77214a0bd3407e68", func(i *inventory.Inventory) bool {
			return len(i.MakeFastMining(8, 13, -1)) > 0
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			mem := plant()
			before := image(mem)
			require.True(t, c.run(inventory.New(mem, life)), "the write was refused")
			require.NotEqual(t, before, image(mem), "the write changed nothing")
			require.Equal(t, c.digest, image(mem),
				"different bytes; if that was deliberate, update the digest")
		})
	}
}

/*
The slots a sweep reports it touched are the slots it touched.

Reach reaches everything with an item in it; mining reaches only what has pick
power, which is the two pickaxes and not the sword beside them.
*/
func TestTheSweepsReportTheSlotsTheyTouched(t *testing.T) {
	inv := inventory.New(plant(), life)
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 9, 11}, inv.LongReach(20),
		"reach touched different slots")
	require.Equal(t, []int{0, 11}, inv.MakeFastMining(8, 13, 200),
		"mining touched something that is not a pickaxe")
}

/*
A modifier's arithmetic lands on the same numbers and the same fields.

This is the one place a rounding rule and a base value can disagree without
either side looking wrong, and writing an item is permanent, so both the bytes
and the report are compared.
*/
func TestApplyingAModifier(t *testing.T) {
	base := map[string]float64{
		"damage": 12, "knockback": 5.5, "useanim": 25, "usetime": 20,
		"scale": 1.0, "shootspeed": 0.0, "mana": 0, "crit": 0,
	}
	for _, c := range []struct {
		name    string
		mults   map[string]float64
		digest  string
		written map[string]float64
		skipped []string
	}{
		{
			// Legendary: every field it scales, and the .5 that a rounding rule
			// decides. 12 * 1.15 is 13.8; 25 * 0.9 is 22.5, which round-half-even
			// puts at 22 and round-half-up would put at 23.
			name:   "legendary",
			mults:  map[string]float64{"damage": 1.15, "knockback": 1.15, "usetime": 0.9, "scale": 1.1},
			digest: "89256c273bb6635f",
			written: map[string]float64{
				"damage": 13.799999999999999, "knockback": 6.324999999999999,
				"scale": 1.1, "useanim": 22.5, "usetime": 18,
			},
			skipped: []string{},
		},
		{
			// An additive bonus, which is added to the base and not multiplied.
			name: "sighted", mults: map[string]float64{"crit": 3},
			digest:  "8c7309ea7143faf4",
			written: map[string]float64{"crit": 3}, skipped: []string{},
		},
		{
			// No modifier at all has to put every field back to base, which is
			// what clearing one does.
			name: "none", mults: map[string]float64{}, digest: "b75607d1f8e65675",
			written: map[string]float64{}, skipped: []string{},
		},
		{
			// A bonus with no verified offset is named rather than dropped.
			name:    "unknown bonus",
			mults:   map[string]float64{"damage": 1.1, "armorpen": 5},
			digest:  "7269663a1387794b",
			written: map[string]float64{"damage": 13.200000000000001},
			skipped: []string{"armorpen"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mem := plant()
			got := inventory.New(mem, life).ApplyPrefixStats(9, c.mults, base)
			require.Equal(t, c.digest, image(mem),
				"different item fields; if that was deliberate, update the digest")
			require.Equal(t, c.written, got.Written, "a different report of what was written")
			require.Equal(t, c.skipped, got.Skipped, "a different report of what was skipped")
		})
	}
}

/*
A slot with no object in it is refused, not written to.

The array entry is null, and a write that treated null as an address would land
at the item's field offsets counted from zero -- which is somewhere in the
process, and on a bad day is mapped.
*/
func TestASlotWithNoObjectIsRefused(t *testing.T) {
	mem := plant()
	inv := inventory.New(mem, life)
	before := image(mem)

	_, ok := inv.ReadSlot(12)
	require.False(t, ok, "a slot with no object was read")
	require.False(t, inv.SetStack(12, 5))
	require.False(t, inv.SetPrefix(12, 1))
	require.False(t, inv.SetUseSpeed(12, 8, 8))
	require.False(t, inv.SetFishingPower(12, 20))
	require.Equal(t, []string{"damage"},
		inv.ApplyPrefixStats(12, map[string]float64{"damage": 1.1},
			map[string]float64{"damage": 12}).Skipped,
		"a modifier on a slot with no object reported something else")
	require.Equal(t, before, image(mem), "something was written to a slot with no object")
}

/*
A fishing power that does not fit a byte is refused by both.

Writing it anyway would wrap: a power of 300 becomes 44, and a rod that fishes
worse than it did is not what "make this rod better" looked like.
*/
func TestAFishingPowerOutOfRangeIsRefused(t *testing.T) {
	mem := plant()
	inv := inventory.New(mem, life)
	before := image(mem)

	require.False(t, inv.SetFishingPower(5, 300), "a power over a byte was written")
	require.False(t, inv.SetFishingPower(5, -1), "a negative power was written")
	require.Equal(t, before, image(mem), "a refused power was written anyway")

	require.True(t, inv.SetFishingPower(5, 255), "the largest power was refused")
	require.NotEqual(t, before, image(mem), "the largest power wrote nothing")
}

/*
An inventory whose array pointer is gone reports nothing rather than reading
from zero.

That is what a collection moving the array looks like from here, and it happens.
*/
func TestAMissingArrayIsReported(t *testing.T) {
	mem := plant()
	mem.PokeI32(uint32(life+layoutInventoryPtrOff), 0)
	inv := inventory.New(mem, life)

	_, ok := inv.ArrayAddr()
	require.False(t, ok, "an array that is not there was found")
	require.Empty(t, inv.Slots(), "slots were read through a null array")
	require.Empty(t, inv.FavoritedPotions(1), "potions were read through a null array")
	require.Empty(t, inv.FishingGear().Rods, "rods were read through a null array")
	require.Zero(t, inv.NonemptyCount(), "items were counted through a null array")
	require.False(t, inv.SetStack(0, 1), "a write went through a null array")
}

/*
The held slot is a hotbar slot or nothing.

The field reads as a plain int and the game only ever puts 0..9 in it, so
anything else means the read landed somewhere that is not the field -- on the
wrong player copy, or after the object moved. Taking it at face value makes the
auto-catch look at an inventory slot nobody is holding.
*/
func TestTheHeldSlot(t *testing.T) {
	// 5 is the rod, 0 is the pickaxe, and the other two are numbers the game
	// never writes there.
	for _, c := range []struct {
		held    int32
		slot    int
		isSlot  bool
		holding bool
	}{
		{held: 5, slot: 5, isSlot: true, holding: true},
		{held: 0, slot: 0, isSlot: true},
		{held: 42},
		{held: -1},
	} {
		t.Run(itoa(int(c.held)), func(t *testing.T) {
			mem := plant()
			mem.PokeI32(uint32(life+layoutSelectedItemOff), c.held)
			inv := inventory.New(mem, life)

			slot, ok := inv.SelectedSlot()
			require.Equalf(t, c.isSlot, ok, "%d was judged differently", c.held)
			if ok {
				require.Equal(t, c.slot, slot)
			}
			require.Equalf(t, c.holding, inv.HoldingRod(), "%d: holding a rod", c.held)
		})
	}
}
