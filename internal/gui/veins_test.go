package gui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
The vein watcher is up exactly while the ore extractor patch is on.

The patch gives the game a queue to mine from and nothing else: with no watcher
filling it, breaking an ore takes that one tile. The window shipped that way --
the switch applied the patch and no loop followed it -- which is what this pins.
Another patch being on is not the extractor being on.
*/
func TestTheVeinWatcherFollowsTheOreExtractor(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	t.Cleanup(u.shutdown)

	u.followOreExtractor()
	require.False(t, u.veins.running(), "nothing read yet, and the watcher is up")

	u.px.on = map[string]bool{"mining": true, "ore_extract": false}
	u.followOreExtractor()
	require.False(t, u.veins.running(), "another patch armed the vein watcher")

	u.px.on = map[string]bool{"mining": true, "ore_extract": true}
	u.followOreExtractor()
	require.True(t, u.veins.running(), "the extractor is on and nothing is watching for a broken ore")

	u.px.on["ore_extract"] = false
	u.followOreExtractor()
	require.False(t, u.veins.running(), "the extractor went off and the watcher stayed")

	// The patches becoming unreadable -- the game closed -- is the extractor
	// being off.
	u.px.on = map[string]bool{"ore_extract": true}
	u.followOreExtractor()
	u.px.on = nil
	u.followOreExtractor()
	require.False(t, u.veins.running())
}

// What the watcher sends is the gems box as it stands that round, and what it
// sends on the way down drops the watcher in the worker.
func TestTheVeinWatcherSendsWhatTheGemsBoxSays(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	t.Cleanup(u.shutdown)

	require.Equal(t, []string{"extract-tick", "--json"}, u.veins.argv())
	u.fx.gems = true
	require.Equal(t, []string{"extract-tick", "--json", "--gems"}, u.veins.argv())
	require.Equal(t, client.ExtractStopArgv(), u.veins.stopArgv())
	require.Equal(t, []string{"extract-stop", "--json"}, client.ExtractStopArgv())
}

// The gems box comes back the way it was left.
func TestTheGemsBoxIsRemembered(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	u.fx.gems = true
	u.saveState()

	next := testUI(t)
	next.sh = u.sh // the same settings file
	next.startWatches()
	require.False(t, next.fx.gems)
	next.loadState()
	require.True(t, next.fx.gems)

	next.shutdown()
	u.shutdown()
}

// The Effects section says what vein mining is and where its switch lives, and
// builds with the watcher in either state.
func TestTheEffectsSectionBuildsWithTheVeinCard(t *testing.T) {
	u := testUI(t)
	u.startWatches()
	t.Cleanup(u.shutdown)

	require.NotNil(t, u.veinCard())
	u.px.on = map[string]bool{"ore_extract": true}
	u.followOreExtractor()
	require.NotNil(t, u.veinCard())
	require.NotNil(t, u.buildEffects())
}
