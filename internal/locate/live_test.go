package locate

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
)

// Every way an entry names has a module; a missing one would quietly find
// nothing, which reads as a game that has not loaded a world.
func TestEveryEntrysWayOfFindingTheLivePlayerExists(t *testing.T) {
	for _, e := range layout.Entries {
		_, ok := liveFinders[e.LocalPlayer]
		require.True(t, ok, "%s finds the live player %q, which has no module", e.Name, e.LocalPlayer)
	}
}

// The zero entry an unsupported runtime selects names no way, and finds nothing.
func TestTheZeroEntryFindsNoLivePlayer(t *testing.T) {
	_, ok := With(layout.Entry{}).Live().Find(nil, nil)
	require.False(t, ok)
}
