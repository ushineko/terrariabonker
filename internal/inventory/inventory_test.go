package inventory_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/inventory"
)

/*
The two inventories read the same items and leave the same bytes behind.

An item's fields are two hundred and fifty bytes of adjacent integers, and a
reader that is one field out gets a plausible number from the wrong place --
useTime read as stack, defense read as headSlot. Nothing here asserts a number
this produced: the Python is asked over the same planted image, and every write
is compared as the whole buffer afterwards, so a field written to the wrong
offset fails as surely as one written with the wrong value.
*/

const pythonTimeout = time.Minute

var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

func askPython(t *testing.T, script string, into any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script) //nolint:gosec // a generated fixture
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "ModuleNotFoundError") {
		t.Skip("the Python package is not importable here")
	}
	require.NoErrorf(t, err, "asking the Python: %s", out)
	require.NoError(t, json.Unmarshal(out, into))
}

// asJSON is a Go value as the Python would print it, for comparing two shapes
// without asserting either one's spelling.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// Every field of every slot is read from the same place.
func TestReadingSlotsMatchesThePython(t *testing.T) {
	var want any
	askPython(t, preamble()+`
print(json.dumps([{
    "index": s.index, "item_addr": s.item_addr, "type": s.type, "stack": s.stack,
    "use_time": s.use_time, "use_anim": s.use_anim, "pick": s.pick,
    "tile_boost": s.tile_boost, "damage": s.damage, "auto_reuse": s.auto_reuse,
    "rare": s.rare, "defense": s.defense, "prefix": s.prefix, "flags": s.flags,
} for s in inv.slots()]))`, &want)

	inv := inventory.New(plant(), life)
	require.Equal(t, want, asJSON(t, inv.Slots()), "the two read different slots")
}

// The sweeps over the whole inventory agree: which slots hold what.
func TestTheSweepsMatchThePython(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
gear = inv.fishing_gear()
print(json.dumps({
    "potions": inv.favorited_potions(),
    "potions_min2": inv.favorited_potions(2),
    "rods": gear["rods"], "baits": gear["baits"],
    "selected": inv.selected_slot(), "holding_rod": inv.holding_rod(),
    "nonempty": inv.nonempty_count(),
    "pickaxes": inv.find_type(3509), "missing": inv.find_type(999999),
}))`, &want)

	inv := inventory.New(plant(), life)

	// The Python reports a potion as a pair and a rod as a triple, so the
	// comparison is against the numbers rather than the shape each side chose.
	potions := func(min int32) [][]int32 {
		var out [][]int32
		for _, p := range inv.FavoritedPotions(min) {
			out = append(out, []int32{int32(p.Slot), p.Buff})
		}
		return out
	}
	require.Equal(t, want["potions"], asJSON(t, potions(1)), "different favorited potions")
	require.Equal(t, want["potions_min2"], asJSON(t, potions(2)), "a stack gate differs")

	gear := inv.FishingGear()
	var rods [][]int32
	for _, r := range gear.Rods {
		rods = append(rods, []int32{int32(r.Slot), int32(r.Power)})
	}
	var baits [][]int32
	for _, b := range gear.Baits {
		baits = append(baits, []int32{int32(b.Slot), int32(b.Power), b.Stack})
	}
	require.Equal(t, want["rods"], asJSON(t, rods), "different rods")
	require.Equal(t, want["baits"], asJSON(t, baits), "different baits")

	slot, ok := inv.SelectedSlot()
	require.True(t, ok, "the held slot did not read")
	require.Equal(t, want["selected"], asJSON(t, slot), "a different held slot")
	require.Equal(t, want["holding_rod"], inv.HoldingRod(), "disagree about holding a rod")
	require.Equal(t, want["nonempty"], asJSON(t, inv.NonemptyCount()), "a different item count")
	require.Equal(t, want["pickaxes"], asJSON(t, inv.FindType(3509)), "a type was found elsewhere")
	require.Equal(t, want["missing"], asJSON(t, inv.FindType(999999)), "a type nobody has was found")
}

// Every write lands on the same bytes.
func TestWritingItemsMatchesThePython(t *testing.T) {
	cases := []struct {
		name   string
		python string
		run    func(inv *inventory.Inventory) bool
	}{
		{"stack", "inv.set_stack(1, 99)", func(i *inventory.Inventory) bool { return i.SetStack(1, 99) }},
		{"type", "inv.set_type(13, 3507)", func(i *inventory.Inventory) bool { return i.SetType(13, 3507) }},
		{"damage", "inv.set_damage(9, 500)", func(i *inventory.Inventory) bool { return i.SetDamage(9, 500) }},
		{"pick", "inv.set_pick(0, 200)", func(i *inventory.Inventory) bool { return i.SetPick(0, 200) }},
		{"defense", "inv.set_defense(9, 12)", func(i *inventory.Inventory) bool { return i.SetDefense(9, 12) }},
		{"tile boost", "inv.set_tile_boost(0, 20)", func(i *inventory.Inventory) bool { return i.SetTileBoost(0, 20) }},
		{"auto reuse on", "inv.set_auto_reuse(9, True)", func(i *inventory.Inventory) bool { return i.SetAutoReuse(9, true) }},
		{"auto reuse off", "inv.set_auto_reuse(0, False)", func(i *inventory.Inventory) bool { return i.SetAutoReuse(0, false) }},
		{"prefix", "inv.set_prefix(9, 81)", func(i *inventory.Inventory) bool { return i.SetPrefix(9, 81) }},
		// A prefix over a byte is masked rather than refused, because the game's
		// own field is a byte and the caller's number came from a list of them.
		{"prefix wraps", "inv.set_prefix(9, 0x141)", func(i *inventory.Inventory) bool { return i.SetPrefix(9, 0x141) }},
		{"use speed", "inv.set_use_speed(0, 8, 13)", func(i *inventory.Inventory) bool { return i.SetUseSpeed(0, 8, 13) }},
		{"fishing power", "inv.set_fishing_power(5, 200)", func(i *inventory.Inventory) bool { return i.SetFishingPower(5, 200) }},
		{"long reach", "inv.long_reach(20)", func(i *inventory.Inventory) bool { return len(i.LongReach(20)) > 0 }},
		{"fast mining", "inv.make_fast_mining(8, 13, 200)", func(i *inventory.Inventory) bool { return len(i.MakeFastMining(8, 13, 200)) > 0 }},
		{"fast mining, power left alone", "inv.make_fast_mining(8, 13, None)", func(i *inventory.Inventory) bool {
			return len(i.MakeFastMining(8, 13, -1)) > 0
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				Buf string `json:"buf"`
			}
			askPython(t, preamble()+c.python+`
print(json.dumps({"buf": mem.buf.hex()}))`, &want)

			mem := plant()
			require.True(t, c.run(inventory.New(mem, life)), "the write was refused")
			require.Equal(t, want.Buf, mem.Hex(), "the two left different memory behind")
		})
	}
}

// The slots a sweep reports it touched are the same slots.
func TestTheSweepsReportTheSameSlots(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
print(json.dumps({"reach": inv.long_reach(20), "mining": inv.make_fast_mining()}))`, &want)

	inv := inventory.New(plant(), life)
	require.Equal(t, want["reach"], asJSON(t, inv.LongReach(20)), "reach touched different slots")
	require.Equal(t, want["mining"], asJSON(t, inv.MakeFastMining(8, 13, 200)), "mining touched different slots")
}

/*
A modifier's arithmetic lands on the same numbers and the same fields.

This is the one place a rounding rule and a base value can disagree without
either side looking wrong, and writing an item is permanent, so both the bytes
and the report are compared.
*/
func TestApplyingAModifierMatchesThePython(t *testing.T) {
	const pythonBase = `{"damage": 12, "knockback": 5.5, "useanim": 25, "usetime": 20,
                          "scale": 1.0, "shootspeed": 0.0, "mana": 0, "crit": 0}`
	goBase := map[string]float64{
		"damage": 12, "knockback": 5.5, "useanim": 25, "usetime": 20,
		"scale": 1.0, "shootspeed": 0.0, "mana": 0, "crit": 0,
	}

	cases := []struct {
		name   string
		python string
		mults  map[string]float64
	}{
		// Legendary: every field it scales, and the .5 that a rounding rule
		// decides. 12 * 1.15 is 13.8; 25 * 0.9 is 22.5, which round-half-even
		// puts at 22 and round-half-up would put at 23.
		{"legendary", `{"damage": 1.15, "knockback": 1.15, "usetime": 0.9, "scale": 1.1}`,
			map[string]float64{"damage": 1.15, "knockback": 1.15, "usetime": 0.9, "scale": 1.1}},
		// An additive bonus, which is added to the base and not multiplied.
		{"sighted", `{"crit": 3}`, map[string]float64{"crit": 3}},
		// No modifier at all has to put every field back to base, which is what
		// clearing one does.
		{"none", `{}`, map[string]float64{}},
		// A bonus with no verified offset is named rather than dropped.
		{"unknown bonus", `{"damage": 1.1, "armorpen": 5}`,
			map[string]float64{"damage": 1.1, "armorpen": 5}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				Buf    string         `json:"buf"`
				Result map[string]any `json:"result"`
			}
			askPython(t, preamble()+`
res = inv.apply_prefix_stats(9, `+c.python+`, `+pythonBase+`)
print(json.dumps({"buf": mem.buf.hex(), "result": res}))`, &want)

			mem := plant()
			got := inventory.New(mem, life).ApplyPrefixStats(9, c.mults, goBase)
			require.Equal(t, want.Buf, mem.Hex(), "the two wrote different item fields")
			require.Equal(t, want.Result["written"], asJSON(t, got.Written), "a different report of what was written")
			require.Equal(t, want.Result["skipped"], asJSON(t, got.Skipped), "a different report of what was skipped")
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
	var want map[string]any
	askPython(t, preamble()+`
print(json.dumps({
    "read": inv.read_slot(12),
    "stack": inv.set_stack(12, 5), "prefix": inv.set_prefix(12, 1),
    "speed": inv.set_use_speed(12, 8, 8), "power": inv.set_fishing_power(12, 20),
    "applied": inv.apply_prefix_stats(12, {"damage": 1.1}, {"damage": 12}),
}))`, &want)
	require.Nil(t, want["read"], "the Python read a slot with no object")

	mem := plant()
	inv := inventory.New(mem, life)
	before := mem.Hex()

	_, ok := inv.ReadSlot(12)
	require.False(t, ok, "a slot with no object was read")
	require.Equal(t, want["stack"], inv.SetStack(12, 5))
	require.Equal(t, want["prefix"], inv.SetPrefix(12, 1))
	require.Equal(t, want["speed"], inv.SetUseSpeed(12, 8, 8))
	require.Equal(t, want["power"], inv.SetFishingPower(12, 20))
	require.Equal(t, want["applied"].(map[string]any)["skipped"],
		asJSON(t, inv.ApplyPrefixStats(12, map[string]float64{"damage": 1.1}, map[string]float64{"damage": 12}).Skipped))
	require.Equal(t, before, mem.Hex(), "something was written to a slot with no object")
}

/*
A fishing power that does not fit a byte is refused by both.

Writing it anyway would wrap: a power of 300 becomes 44, and a rod that fishes
worse than it did is not what "make this rod better" looked like.
*/
func TestAFishingPowerOutOfRangeIsRefused(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
print(json.dumps({"over": inv.set_fishing_power(5, 300),
                  "under": inv.set_fishing_power(5, -1),
                  "edge": inv.set_fishing_power(5, 255), "buf": mem.buf.hex()}))`, &want)

	mem := plant()
	inv := inventory.New(mem, life)
	require.Equal(t, want["over"], inv.SetFishingPower(5, 300), "a power over a byte disagrees")
	require.Equal(t, want["under"], inv.SetFishingPower(5, -1), "a negative power disagrees")
	require.Equal(t, want["edge"], inv.SetFishingPower(5, 255), "the largest power disagrees")
	require.Equal(t, want["buf"], mem.Hex(), "the two left different memory behind")
}

/*
An inventory whose array pointer is gone reports nothing rather than reading
from zero.

That is what a collection moving the array looks like from here, and it happens.
*/
func TestAMissingArrayIsReported(t *testing.T) {
	var want map[string]any
	askPython(t, preamble()+`
mem.poke_i32(`+itoa(life)+` + I.INVENTORY_PTR_OFF, 0)
print(json.dumps({"addr": inv.array_addr(), "slots": inv.slots(),
                  "potions": inv.favorited_potions(), "gear": inv.fishing_gear(),
                  "nonempty": inv.nonempty_count(), "set": inv.set_stack(0, 1)}))`, &want)
	require.Nil(t, want["addr"], "the Python found an array that is not there")

	mem := plant()
	mem.PokeI32(uint32(life+layoutInventoryPtrOff), 0)
	inv := inventory.New(mem, life)

	_, ok := inv.ArrayAddr()
	require.False(t, ok, "an array that is not there was found")
	require.Empty(t, inv.Slots(), "slots were read through a null array")
	require.Empty(t, inv.FavoritedPotions(1), "potions were read through a null array")
	require.Empty(t, inv.FishingGear().Rods, "rods were read through a null array")
	require.Equal(t, want["nonempty"], asJSON(t, inv.NonemptyCount()))
	require.Equal(t, want["set"], inv.SetStack(0, 1), "a write went through a null array")
}

/*
The held slot is a hotbar slot or nothing.

The field reads as a plain int and the game only ever puts 0..9 in it, so
anything else means the read landed somewhere that is not the field -- on the
wrong player copy, or after the object moved. Taking it at face value makes the
auto-catch look at an inventory slot nobody is holding.
*/
func TestTheHeldSlotMatchesThePython(t *testing.T) {
	// 5 is the rod, 0 is the pickaxe, and the other two are numbers the game
	// never writes there.
	for _, held := range []int32{5, 0, 42, -1} {
		t.Run(itoa(int(held)), func(t *testing.T) {
			var want map[string]any
			askPython(t, preamble()+`
mem.poke_i32(`+itoa(life)+` + I.SELECTED_ITEM_OFF, `+itoa(int(held))+`)
print(json.dumps({"slot": inv.selected_slot(), "rod": inv.holding_rod()}))`, &want)

			mem := plant()
			mem.PokeI32(uint32(life+layoutSelectedItemOff), held)
			inv := inventory.New(mem, life)

			slot, ok := inv.SelectedSlot()
			if want["slot"] == nil {
				require.Falsef(t, ok, "%d was taken for a hotbar slot", held)
			} else {
				require.Truef(t, ok, "%d is a hotbar slot there and not here", held)
				require.Equal(t, want["slot"], asJSON(t, slot))
			}
			require.Equal(t, want["rod"], inv.HoldingRod(), "disagree about holding a rod")
		})
	}
}
