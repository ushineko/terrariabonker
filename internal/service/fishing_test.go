package service_test

import (
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

// A floor below one is refused: topping a stack up to nothing is not a thing to
// ask for.
func TestABaitFloorBelowOneIsRefused(t *testing.T) {
	atHome(t)
	_, err := service.New(plant(), -1).BaitTick(0)
	require.Error(t, err)
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
