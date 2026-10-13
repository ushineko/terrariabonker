package gui

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
)

/*
The sell log names the price of each stack.

The reply is built from the service's own type and not from a string written
here, because the log once read a field the reply does not have and said
"for <nil>" under every sale. A reply spelled out by hand would have carried
the same wrong name and passed.
*/
func TestTheSellLogSaysWhatEachStackSoldFor(t *testing.T) {
	reply, err := json.Marshal(map[string]any{"sold": []service.Sold{
		{Slot: 45, Type: 3457, Name: "Nebula Fragment", Stack: 45, Copper: 90000},
		{Slot: 34, Type: 23, Name: "Gel", Stack: 60, Copper: 60},
		{Slot: 3, Type: 2, Name: "Dirt Block", Stack: 9, Copper: 0},
	}})
	require.NoError(t, err)

	require.Equal(t, []string{
		"sold Nebula Fragment x45 for 9 gold",
		"sold Gel x60 for 60 copper",
		"sold Dirt Block x9 for nothing",
	}, soldLines(string(reply)))
}

// Copper is written as the game writes it, largest coin first, with nothing
// said about a coin there are none of.
func TestCopperIsWrittenAsCoins(t *testing.T) {
	require.Equal(t, "1 platinum 23 gold 45 silver 6 copper", coinText(1234506))
	require.Equal(t, "15 gold 60 silver 77 copper", coinText(156077))
	require.Equal(t, "2 silver", coinText(200))
	require.Equal(t, "nothing", coinText(0))
}
