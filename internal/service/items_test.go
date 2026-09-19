package service_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Changing what is in a slot, which is the operation that can destroy something.

An item is not its type: setting the type alone gives a name with nothing behind
it, because the stats are assigned when the game builds the item. So a change of
item copies the block out of the game's own pristine template -- and getting that
wrong writes a template over a real item.

Two guards here exist because each failed once. The slot is read from the *live*
copy, not the first one found, because a snapshot holds whatever was in it when
it was taken. And the caller says what it believed the slot held, so an item the
game moved in the meantime is refused rather than overwritten.
*/

// The template the fixture plants, and the item it is a template for.
const (
	templateType = 757
	templateAt   = base + 0x28000
)

// plantTemplate puts a pristine template for one item type in the heap, behind
// the same vtable the player's own items carry.
func plantTemplateInto(mem *execMem) {
	// Behind the same vtable the player's own items carry, because that is what
	// the search recognises an item object by.
	mem.PokeBytes(templateAt, u32(itemVTable))
	mem.PokeI32(templateAt+uint32(layoutItemType), templateType)
	mem.PokeI32(templateAt+uint32(layoutItemDamage), 40)
	mem.PokeI32(templateAt+uint32(layoutItemUseTime), 24)
	mem.PokeI32(templateAt+uint32(layoutItemUseAnim), 24)
	mem.PokeI32(templateAt+uint32(layoutItemRare), 8)
	mem.WriteF32(templateAt+uint32(layoutItemKnockback), 6.5)
	mem.WriteF32(templateAt+uint32(layoutItemScale), 1.0)
	/*
		And the three equipment slots, which a template holds as -1.

		Zero is a real slot -- the first helmet -- so a template left zeroed
		reads as armour whatever else is on it, and the catalog files every
		weapon in the game under "Armor".
	*/
	for _, field := range []string{"ITEM_HEAD_SLOT", "ITEM_BODY_SLOT", "ITEM_LEG_SLOT"} {
		mem.PokeI32(templateAt+uint32(layout.Offsets[field]), -1) //nolint:gosec // an offset
	}

	/*
		And templates for what the fishing kit hands out.

		Without them a given rod or bait is a bare type with none of the fields
		that make it one -- so the kit would hand out something the gear sweep
		does not recognise, and hand out another every time it was asked.
	*/
	plantKitTemplate(mem, kitRodAt, service.KitRod, "ITEM_FISHING_POLE", 50)
	plantKitTemplate(mem, kitBaitAt, service.KitBait, "ITEM_BAIT", 35)
}

// Where the kit's own templates go.
const (
	kitRodAt  = base + 0x2A000
	kitBaitAt = base + 0x2C000
)

// plantKitTemplate is a template for one of the kit's items, with the byte that
// makes it what it is.
func plantKitTemplate(mem *execMem, at uint32, itemType int32, field string, power byte) {
	mem.PokeBytes(at, u32(itemVTable))
	mem.PokeI32(at+uint32(layoutItemType), itemType)
	mem.PokeBytes(at+uint32(layout.Offsets[field]), []byte{power}) //nolint:gosec // an offset
}

// pyPlantTemplate is the same, as Python source.
func pyPlantTemplate() string {
	return fmt.Sprintf(`
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + I.ITEM_TYPE, %d)
mem.poke_i32(%d + I.ITEM_DAMAGE, 40)
mem.poke_i32(%d + I.ITEM_USE_TIME, 24)
mem.poke_i32(%d + I.ITEM_USE_ANIM, 24)
mem.poke_i32(%d + I.ITEM_RARE, 8)
mem.write_f32(%d + I.ITEM_KNOCKBACK, 6.5)
mem.write_f32(%d + I.ITEM_SCALE, 1.0)
mem.poke_i32(%d + I.ITEM_HEAD_SLOT, -1)
mem.poke_i32(%d + I.ITEM_BODY_SLOT, -1)
mem.poke_i32(%d + I.ITEM_LEG_SLOT, -1)
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + I.ITEM_TYPE, %d)
mem.poke_bytes(%d + I.ITEM_FISHING_POLE, bytes([50]))
mem.poke_bytes(%d, struct.pack("<I", %d))
mem.poke_i32(%d + I.ITEM_TYPE, %d)
mem.poke_bytes(%d + I.ITEM_BAIT, bytes([35]))
`, templateAt, itemVTable, templateAt, templateType, templateAt, templateAt,
		templateAt, templateAt, templateAt, templateAt,
		templateAt, templateAt, templateAt,
		kitRodAt, itemVTable, kitRodAt, service.KitRod, kitRodAt,
		kitBaitAt, itemVTable, kitBaitAt, service.KitBait, kitBaitAt)
}

/*
Setting a slot to a different item leaves the same bytes.

The whole buffer is compared, so a template written at the wrong offset or into
the wrong copy shows up as surely as a wrong value.
*/
func TestSetItemMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name   string
		python string
		run    func(*service.Service) error
	}{
		{"a different item, which is templated", fmt.Sprintf("svc.set_item(1, %d)", templateType),
			func(s *service.Service) error { return s.SetItem(1, templateType, service.ItemEdit{}) }},
		{"the same item, which is not", "svc.set_item(1, 188, stack=42)",
			func(s *service.Service) error {
				stack := int32(42)
				return s.SetItem(1, 188, service.ItemEdit{Stack: &stack})
			}},
		{"clearing a slot", "svc.set_item(1, 0)",
			func(s *service.Service) error { return s.SetItem(1, 0, service.ItemEdit{}) }},
		{"an item with fields set on top", fmt.Sprintf(
			"svc.set_item(1, %d, damage=500, use_time=4, pick=200, defense=12)", templateType),
			func(s *service.Service) error {
				d, u, p, def := int32(500), int32(4), int32(200), int32(12)
				return s.SetItem(1, templateType, service.ItemEdit{
					Damage: &d, UseTime: &u, Pick: &p, Defense: &def,
				})
			}},
		{"a type there is no template for", "svc.set_item(1, 4242)",
			func(s *service.Service) error { return s.SetItem(1, 4242, service.ItemEdit{}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want string
			askPython(t, preamble()+pyPlantTemplate()+c.python+`
print(json.dumps(mem.buf.hex()))`, &want)

			mem := plant()
			plantTemplateInto(mem)
			require.NoError(t, c.run(service.New(mem, -1)))
			sameMemory(t, want, mem.Hex(), "the two left different memory behind")
		})
	}
}

/*
An item that changed under the caller is refused, not overwritten.

The caller states what it believed the slot held. If the game moved items since
they looked, writing would template their stale item over whatever is really
there and destroy it.
*/
func TestASlotThatChangedIsRefused(t *testing.T) {
	mem := plant()
	plantTemplateInto(mem)
	svc := service.New(mem, -1)
	before := mem.Hex()

	// Slot 1 holds a potion in the fixture, not the staff the caller believes.
	stale := int32(templateType)
	err := svc.SetItem(1, 9, service.ItemEdit{ExpectType: &stale})
	require.Error(t, err, "a stale edit was written")
	require.Contains(t, err.Error(), "changed in-game")
	sameMemory(t, before, mem.Hex(), "something was written anyway")

	// Told what is really there, it goes ahead.
	real := int32(188)
	require.NoError(t, svc.SetItem(1, 9, service.ItemEdit{ExpectType: &real}),
		"a correct expectation was refused")
	require.NotEqual(t, before, mem.Hex(), "nothing was written")
}

/*
The slot the caller is checked against is the live copy's.

The copies are not identical: a snapshot holds whatever was in the slot when it
was taken. Checking against the wrong one is how editing a hotbar slot came to be
refused for holding one torch while the game and the grid both showed another.
*/
func TestTheExpectationIsCheckedAgainstTheLiveCopy(t *testing.T) {
	mem := plant()
	plantTemplateInto(mem)
	svc := service.New(mem, -1)

	// The inert snapshot found first holds an axe in slot 1; the live player
	// holds a potion.
	live := int32(188)
	require.NoError(t, svc.SetItem(1, 9, service.ItemEdit{ExpectType: &live}),
		"the live copy's item was not what the expectation was checked against")

	snapshot := int32(3506)
	err := svc.SetItem(1, 9, service.ItemEdit{ExpectType: &snapshot})
	require.Error(t, err, "the snapshot's item was accepted as the live one")
}

// An item given lands in the first empty main slot, in every copy.
func TestGiveItemMatchesThePython(t *testing.T) {
	var want struct {
		Slot int    `json:"slot"`
		Buf  string `json:"buf"`
	}
	askPython(t, preamble()+pyPlantTemplate()+fmt.Sprintf(`
slot = svc.give_item(%d, 5)
print(json.dumps({"slot": slot, "buf": mem.buf.hex()}))`, templateType), &want)

	mem := plant()
	plantTemplateInto(mem)
	slot, err := service.New(mem, -1).GiveItem(templateType, 5)
	require.NoError(t, err)
	require.Equal(t, want.Slot, slot, "the item landed in a different slot")
	sameMemory(t, want.Buf, mem.Hex(), "the two left different memory behind")
}

/*
Which slot is free is read from the live copy.

A snapshot shows whatever was there when it was taken, so trusting it can pick a
slot that is empty in the snapshot and occupied in the game -- and the give lands
on top of a real item and destroys it.
*/
func TestGiveUsesTheLiveCopysFreeSlot(t *testing.T) {
	mem := plant()
	plantTemplateInto(mem)

	slot, err := service.New(mem, -1).GiveItem(templateType, 1)
	require.NoError(t, err)

	// Whatever slot it chose has to be one the *live* player had empty.
	live, err := service.New(mem, -1).Inventory()
	require.NoError(t, err)
	for _, s := range live {
		if s.Slot == slot {
			require.Equal(t, int32(templateType), s.Type,
				"the item did not land where the live copy had room")
		}
	}
	require.Less(t, slot, service.GiveSlots, "the item landed outside the main grid")
}

// A pristine template's stats are read from the template, not from a live item.
func TestPrefixBaseStatsMatchThePython(t *testing.T) {
	var want map[string]float64
	askPython(t, preamble()+pyPlantTemplate()+fmt.Sprintf(`
print(json.dumps(svc._prefix_base_stats(%d)))`, templateType), &want)
	require.NotEmpty(t, want, "the Python read no base stats from a planted template")

	mem := plant()
	plantTemplateInto(mem)
	got := service.New(mem, -1).PrefixBaseStats(templateType)

	require.Len(t, got, len(want), "a different number of fields was read")
	for key, v := range want {
		require.InDeltaf(t, v, got[key], 1e-6, "%s reads differently", key)
	}
	require.InDelta(t, 40.0, got["damage"], 0, "the damage did not come from the template")
	require.InDelta(t, 6.5, got["knockback"], 1e-6, "a float field did not read")
}

/*
A modifier writes the bonuses it confers, not just its name.

The prefix byte is only what the tooltip prints. The game multiplies the bonuses
into the item's own fields, which is why setting the byte alone produced a Godly
weapon with nothing Godly about it.

The bonuses come from the item's *pristine* stats rather than its current ones,
which is what makes applying one twice give the same answer and switching between
two give what the second would have given on a fresh item.
*/
func TestApplyingAModifierMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name   string
		prefix int32
	}{
		{"Godly, which scales damage and knockback", 59},
		{"Quick, which scales the use time", 5},
		{"none, which puts everything back", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want string
			askPython(t, preamble()+pyPlantTemplate()+fmt.Sprintf(`
svc.set_item(1, %d, prefix=%d)
print(json.dumps(mem.buf.hex()))`, templateType, c.prefix), &want)

			mem := plant()
			plantTemplateInto(mem)
			prefix := c.prefix
			require.NoError(t, service.New(mem, -1).SetItem(1, templateType,
				service.ItemEdit{Prefix: &prefix}))
			sameMemory(t, want, mem.Hex(), "the two left different memory behind")
		})
	}
}

/*
Applying the same modifier twice gives the same item.

The bonuses are scaled from the template, so a re-apply cannot compound. Scaling
the item's current values instead would drift a little further every time -- and
the window re-applies on every edit.
*/
func TestAModifierDoesNotCompound(t *testing.T) {
	mem := plant()
	plantTemplateInto(mem)
	svc := service.New(mem, -1)
	godly := int32(59)

	require.NoError(t, svc.SetItem(1, templateType, service.ItemEdit{Prefix: &godly}))
	once := mem.Hex()

	require.NoError(t, svc.SetItem(1, templateType, service.ItemEdit{Prefix: &godly}))
	sameMemory(t, once, mem.Hex(), "applying the same modifier twice changed the item again")
}

/*
A field the caller typed wins over the one the modifier computed.

The modifier goes on first and the explicit edits after, so somebody who asks for
a Godly weapon doing 500 damage gets 500 rather than whatever Godly worked out.
*/
func TestATypedFieldWinsOverTheModifier(t *testing.T) {
	mem := plant()
	plantTemplateInto(mem)
	godly, damage := int32(59), int32(500)

	require.NoError(t, service.New(mem, -1).SetItem(1, templateType,
		service.ItemEdit{Prefix: &godly, Damage: &damage}))

	inv, err := service.New(mem, -1).Inventory()
	require.NoError(t, err)
	for _, slot := range inv {
		if slot.Slot == 1 {
			require.Equal(t, int32(500), slot.Damage,
				"the modifier's damage won over the one that was asked for")
			require.Equal(t, int32(59), slot.Prefix, "the modifier was not applied")
		}
	}
}
