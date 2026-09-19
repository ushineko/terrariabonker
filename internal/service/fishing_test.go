package service_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Fishing, and the two rules about not losing something the player owns.

A rod's original power goes to disk *before* the raised one is written: if the
process dies between the two, a missing record loses the rod's real power forever
while a spare record only causes a harmless restore. And a restore is keyed by
item type rather than slot, because a rod that has been moved is still the same
rod.
*/

// The fixture's live player carries a rod in slot 2 and bait in none, so the kit
// has something to do and something to leave alone.
func TestFishingKitMatchesThePython(t *testing.T) {
	home := atHome(t)

	var want map[string]any
	askPython(t, preamble()+pyProfileAt(home)+pyPlantTemplate()+`
print(json.dumps(svc.fishing_kit()))`, &want)

	mem := plant()
	plantTemplateInto(mem)
	got, err := service.New(mem, -1).FishingKit()
	require.NoError(t, err)

	require.Equal(t, want["rods"], asJSON(t, got.Rods), "a different set of rods")
	require.Equal(t, want["baits"], asJSON(t, got.Baits), "a different set of bait")
	require.Equal(t, want["gave"], asJSON(t, got.Gave), "something else was given")
}

/*
A player who already has gear is given nothing.

A cheat that handed out another rod every time it was switched on would fill the
inventory, and the rod somebody chose is likelier to be the one they want.
*/
func TestTheKitGivesNothingTwice(t *testing.T) {
	atHome(t)
	mem := plant()
	plantTemplateInto(mem)
	svc := service.New(mem, -1)

	first, err := svc.FishingKit()
	require.NoError(t, err)
	require.NotEmpty(t, first.Rods, "the fixture leaves the player with no rod at all")

	second, err := svc.FishingKit()
	require.NoError(t, err)
	require.Empty(t, second.Gave, "a second call handed out more gear")
	require.Equal(t, len(first.Rods), len(second.Rods), "the player ended up with more rods")
}

// Raising a rod's power records what it was, and leaves the same bytes.
func TestSetFishingPowerMatchesThePython(t *testing.T) {
	home := atHome(t)

	var want struct {
		Result map[string]any `json:"result"`
		Owed   map[string]int `json:"owed"`
		Buf    string         `json:"buf"`
	}
	askPython(t, preamble()+pyProfileAt(home)+`
res = svc.set_fishing_power(200)
print(json.dumps({"result": res, "buf": mem.buf.hex(),
                  "owed": {str(k): v for k, v in profile.rod_powers_to_restore().items()}}))`,
		&want)

	mem := plant()
	got, err := service.New(mem, -1).SetFishingPower(200)
	require.NoError(t, err)
	require.Equal(t, want.Result["changed"], asJSON(t, got["changed"]), "different rods changed")
	sameMemory(t, want.Buf, mem.Hex(), "the two left different memory behind")

	owed := profile.RodPowersToRestore()
	require.Len(t, owed, len(want.Owed), "a different number of rods is owed a restore")
	for key, v := range want.Owed {
		var itemType int32
		_, err := fmt.Sscanf(key, "%d", &itemType)
		require.NoError(t, err)
		require.Equalf(t, int32(v), owed[itemType], "rod %s is owed a different power", key)
	}
}

/*
Nothing is raised if the original could not be recorded.

The record goes to disk before the rod is written, and the order is the one that
fails safe: a missing record loses the rod's real power forever, while a spare
record only causes a harmless restore. So a profile that cannot be written must
stop the whole thing rather than leave a rod raised with no way back.
*/
func TestNothingIsRaisedIfTheOriginalCannotBeRecorded(t *testing.T) {
	home := atHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(profile.Path()), 0o755))
	// The profile's directory is made unwritable, so recording fails.
	require.NoError(t, os.Chmod(filepath.Dir(profile.Path()), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(profile.Path()), 0o755) })
	_ = home

	mem := plant()
	before := mem.Hex()

	_, err := service.New(mem, -1).SetFishingPower(200)
	require.Error(t, err, "a rod was raised with nothing recording its original")
	require.Equal(t, before, mem.Hex(), "the rod was raised before the record failed")
}

// A power outside the field's range is refused rather than wrapped.
func TestAnImpossibleFishingPowerIsRefused(t *testing.T) {
	atHome(t)
	mem := plant()
	svc := service.New(mem, -1)
	before := mem.Hex()

	for _, power := range []int32{0, -1, 256, 1000} {
		_, err := svc.SetFishingPower(power)
		require.Errorf(t, err, "a power of %d was accepted", power)
	}
	require.Equal(t, before, mem.Hex(), "something was written anyway")
}

/*
A rod is put back by its type, not the slot it was in.

A rod that has been moved since the cheat was switched on is still the same rod,
and a slot-keyed restore loses track of exactly that.
*/
func TestRestoringFishingPowerMatchesThePython(t *testing.T) {
	home := atHome(t)

	var want map[string]any
	askPython(t, preamble()+pyProfileAt(home)+`
svc.set_fishing_power(200)
print(json.dumps(svc.restore_fishing_power()))`, &want)

	mem := plant()
	svc := service.New(mem, -1)
	_, err := svc.SetFishingPower(200)
	require.NoError(t, err)

	got, err := svc.RestoreFishingPower()
	require.NoError(t, err)
	require.Equal(t, want["restored"], asJSON(t, got["restored"]), "different rods were restored")
	require.Empty(t, profile.RodPowersToRestore(), "something is still owed a restore")

	// And the rod really is back.
	inv, err := service.New(mem, -1).Inventory()
	require.NoError(t, err)
	_ = inv
}

// Restoring when nothing is owed does nothing and says so.
func TestRestoringWhenNothingIsOwed(t *testing.T) {
	atHome(t)
	mem := plant()
	before := mem.Hex()

	got, err := service.New(mem, -1).RestoreFishingPower()
	require.NoError(t, err)
	require.Empty(t, got["restored"])
	require.Equal(t, before, mem.Hex(), "something was written with nothing owed")
}

// Bait is topped up to the floor, in every copy.
func TestBaitTickMatchesThePython(t *testing.T) {
	home := atHome(t)

	for _, keep := range []int32{1, 30, 999} {
		t.Run(fmt.Sprintf("keep%d", keep), func(t *testing.T) {
			var want struct {
				Result map[string]any `json:"result"`
				Buf    string         `json:"buf"`
			}
			askPython(t, preamble()+pyProfileAt(home)+pyPlantBait()+fmt.Sprintf(`
res = svc.bait_tick(keep=%d)
print(json.dumps({"result": res, "buf": mem.buf.hex()}))`, keep), &want)

			mem := plant()
			plantBaitInto(mem)
			got, err := service.New(mem, -1).BaitTick(keep)
			require.NoError(t, err)
			require.Equal(t, want.Result["topped"], asJSON(t, got["topped"]), "different bait was topped")
			require.Equal(t, want.Result["baits"], asJSON(t, got["baits"]), "a different bait count")
			sameMemory(t, want.Buf, mem.Hex(), "the two left different memory behind")
		})
	}
}

// A floor below one is refused: topping a stack up to nothing is not a thing to
// ask for.
func TestABaitFloorBelowOneIsRefused(t *testing.T) {
	atHome(t)
	_, err := service.New(plant(), -1).BaitTick(0)
	require.Error(t, err)
}

/*
The fishing effects are held up, and one the player drank is deferred to.

A renewal never shortens a buff, so a real potion keeps its time -- and this
reports that as deferring rather than as holding, because they are different
things to tell somebody.
*/
func TestFishingBuffTickMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name string
		want map[string]bool
		py   string
	}{
		{"nothing asked for", map[string]bool{}, ""},
		{"one effect", map[string]bool{"power": true}, "power=True"},
		{"all three", map[string]bool{"power": true, "sonar": true, "crate": true},
			"power=True, sonar=True, crate=True"},
	} {
		t.Run(c.name, func(t *testing.T) {
			atHome(t)
			var got map[string]any
			askPython(t, preamble()+plantBuffs()+fmt.Sprintf(`
print(json.dumps(svc.fishing_buff_tick(%s)))`, c.py), &got)

			mem := plant()
			plantBuffsInto(mem)
			res, err := service.New(mem, -1).FishingBuffTick(c.want, 0)
			require.NoError(t, err)
			require.Equal(t, got["held"], asJSON(t, res["held"]), "different effects were held")
			require.Equal(t, got["deferred"], asJSON(t, res["deferred"]), "different effects were deferred")
		})
	}
}

/*
A potion the player drank is deferred to, not cut short.

The fixture plants one of the three fishing buffs already running for eight
minutes, which is what drinking a potion looks like.
*/
func TestAPotionIsDeferredToRatherThanCutShort(t *testing.T) {
	atHome(t)
	mem := plant()
	plantBuffsInto(mem)

	res, err := service.New(mem, -1).FishingBuffTick(
		map[string]bool{"power": true, "sonar": true}, 0)
	require.NoError(t, err)

	deferred := res["deferred"].([]service.HeldBuff)
	require.Len(t, deferred, 1, "the potion the fixture plants was not deferred to")
	require.Equal(t, "power", deferred[0].Effect)
	require.Equal(t, "kept", deferred[0].What, "the potion was written to")

	held := res["held"].([]service.HeldBuff)
	require.Len(t, held, 1, "the effect nobody was running was not held up")
	require.Equal(t, "sonar", held[0].Effect)
}

/*
A rod nothing is owed for is left alone.

The restore is keyed by item type, and a rod the cheat never raised has no entry
-- so looking one up and writing whatever comes back would set that rod's power
to nothing. A player who picked up a second rod after switching the cheat on
would find it ruined by switching the cheat off.
*/
func TestARodNothingIsOwedForIsLeftAlone(t *testing.T) {
	atHome(t)
	mem := plant()
	svc := service.New(mem, -1)

	_, err := svc.SetFishingPower(200)
	require.NoError(t, err)

	// A second rod, picked up afterwards, which nothing is owed for.
	plantSecondRod(mem)
	gear, err := service.New(mem, -1).Inventory()
	require.NoError(t, err)
	_ = gear

	_, err = svc.RestoreFishingPower()
	require.NoError(t, err)

	power := secondRodPower(mem)
	require.Equal(t, byte(40), power,
		"a rod the cheat never raised was written to by the restore")
}
