package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
The two entries' shapes, pinned as literals with where each was measured.

mono: the szarray constants (ArrLenOff/ArrDataOff, from the projectile array
work in docs/discovery.md) and locate.ReadMonoString's 12-byte string header;
statLifeMax2 before statLifeMax is layout.StatLifeMax2Off < StatLifeMaxOff.

CLR (spec 052 phase 0, cmd/winrecon against the live game): all seven of the
maintainer's characters had the Player MethodTable at statLife - 0x470 with
statLife at object offset 0x470 from cmd/clrfields, so fields start at +4;
names decoded with length at +4 and characters at +8; Item[] read MethodTable,
59, then elements at +8; clrfields put statLifeMax at 0x468 and statLifeMax2 at
0x46C.
*/
func TestTheShapesAreWhatWasMeasured(t *testing.T) {
	require.Equal(t, Shapes{ObjectHeader: 8, StringLenOff: 8, StringCharsOff: 12,
		ArrLenOff: 0x0C, ArrDataOff: 0x10, LifeMaxFirst: false}, monoEntry.Shapes)
	require.Equal(t, Shapes{ObjectHeader: 4, StringLenOff: 4, StringCharsOff: 8,
		ArrLenOff: 4, ArrDataOff: 8, LifeMaxFirst: true}, clrEntry.Shapes)
}

/*
The whole table, frozen.

A transcribed copy of a table gets edited to match when the two disagree; a
digest does not. Changing an entry means changing this digest on purpose, in
the same commit, with the measurement that justified it.
*/
func TestTheTableIsFrozen(t *testing.T) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", Entries)))
	require.Equal(t, "c306c0e4693ef59a8956fa2c1d9c0ee5bf64d77052085e98c19d80e2f8826be7",
		hex.EncodeToString(sum[:]), "the version table changed:\n%#v", Entries)
}

func TestSelect(t *testing.T) {
	const build = "1.4.5.8+24893155"
	cases := []struct {
		name           string
		build, runtime string
		want           Support
		entry          string
	}{
		// Linux today: any wine-mono, as before the table existed.
		{"mono, known build", build, "wine-mono-11.3.0", Supported, "wine-mono"},
		{"mono, another version", build, "wine-mono-10.4.1", Supported, "wine-mono"},
		{"mono, older known build", "1.4.5.7+24825745", "wine-mono-11.2.0", Supported, "wine-mono"},
		{"mono, a game update", "1.4.5.9+25000000", "wine-mono-11.3.0", Candidate, "wine-mono"},
		// Windows: the CLR entry, never the mono one in its place. Whether it
		// may write is a separate question (TestReadsAndWritesAreSeparate).
		{"netfx, measured version", build, "netfx-4.8.9345.0", Supported, "netfx-4.8.1"},
		{"netfx, version unreadable", build, "netfx-unknown", Candidate, "netfx-4.8.1"},
		// Undetected is not evidence: the mono numbers, as before the table.
		{"nothing detected", build, "", RuntimeUnknown, "wine-mono"},
		// Detected and unknown is not undetected: nothing here fits it.
		{"a family nobody has seen", build, "coreclr-9.0.0", Unsupported, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, got := Select(c.build, c.runtime)
			require.Equal(t, c.want, got)
			require.Equal(t, c.entry, e.Name)
		})
	}
}

// An enabled CLR entry would be selected for its own build and version, and be
// a candidate for a servicing update -- the behaviour phase 3 turns on.
func TestAnEnabledEntryIsSelectedForItsOwnRuntime(t *testing.T) {
	saved := Entries
	t.Cleanup(func() { Entries = saved })
	clr := clrEntry
	clr.Enabled = true
	Entries = []Entry{monoEntry, clr}

	e, got := Select("1.4.5.8+24893155", "netfx-4.8.9345.0")
	require.Equal(t, Supported, got)
	require.Equal(t, "netfx-4.8.1", e.Name)

	e, got = Select("1.4.5.8+24893155", "netfx-4.8.9400.0")
	require.Equal(t, Candidate, got)
	require.Equal(t, "netfx-4.8.1", e.Name)
}

func TestParseRuntime(t *testing.T) {
	rt, ok := ParseRuntime("wine-mono-11.3.0")
	require.True(t, ok)
	require.Equal(t, Runtime{Family: WineMono, Version: "11.3.0"}, rt)
	rt, ok = ParseRuntime("netfx-4.8.9345.0")
	require.True(t, ok)
	require.Equal(t, Runtime{Family: NetFx, Version: "4.8.9345.0"}, rt)
	for _, bad := range []string{"", "wine-mono-", "netfx", "mono-6.12"} {
		_, ok := ParseRuntime(bad)
		require.False(t, ok, bad)
	}
}

/*
wine-mono 11.3.0 is on the mono entry's ledger, and the ledger is not a gate:
another wine-mono is still an exact match, as it was before anything was
confirmed. Measured on the maintainer's Linux machine, 2026-10-08.
*/
func TestTheConfirmedRuntimeIsALedgerNotAGate(t *testing.T) {
	require.Len(t, monoEntry.Confirmed, 1)
	require.Contains(t, monoEntry.Confirmed[0], "11.3.0: 1.4.5.8+24893155")
	require.Nil(t, monoEntry.Versions)
	_, got := Select("1.4.5.8+24893155", "wine-mono-10.4.1")
	require.Equal(t, Supported, got, "an unconfirmed wine-mono stopped matching")
}

/*
What each entry may read and whether it may write.

The CLR entry reads the player, which copy is live and the inventory, and writes
nothing: every other reader still uses the mono constants, and a write with a
wrong number lands in a live save. The zero entry -- what an unsupported
runtime gets -- can do neither.
*/
func TestReadsAndWritesAreSeparate(t *testing.T) {
	require.True(t, monoEntry.CanRead(ReadPlayer))
	require.True(t, monoEntry.CanRead(ReadInventory))
	require.True(t, monoEntry.Writes)

	require.True(t, clrEntry.CanRead(ReadPlayer))
	require.True(t, clrEntry.CanRead(ReadInventory))
	require.True(t, clrEntry.CanRead(ReadLocalPlayer))
	require.False(t, clrEntry.Writes, "the CLR entry may write before its write paths exist")

	var none Entry
	require.False(t, none.CanRead(ReadPlayer))
	require.False(t, none.Writes)
}

/*
The CLR player fields are the CLR table's own differences, not a second
spelling of them: name and inventory, less statLife.
*/
func TestTheCLRPlayerFieldsAreTheTable(t *testing.T) {
	at := map[string]int{}
	for _, f := range CLRFields["Player"] {
		at[f.Name] = int(f.Offset)
	}
	require.Equal(t, at["name"]-at["statLife"], clrEntry.Player.NameFromLife)
	require.Equal(t, at["inventory"]-at["statLife"], clrEntry.Player.InventoryFromLife)
}

// The mono player fields are the mono constants they always were.
func TestTheMonoPlayerFieldsAreTheConstants(t *testing.T) {
	require.Equal(t, PlayerFields{NameFromLife: -0x6C0, InventoryFromLife: -0x664, SelectedItemFromLife: -0x694}, monoEntry.Player)
}

/*
The CLR statics numbers are the CLR table's own differences, and the array
lengths are what spec 052 phase 0 measured on the live game: the Main.player
array that held the live player had 256 slots, and the one block that also
reached an NPC[] and a Projectile[] reached 201 and 1001.
*/
func TestTheCLRStaticsAreTheTable(t *testing.T) {
	main, player := map[string]int{}, map[string]int{}
	for _, f := range CLRFields["Main"] {
		main[f.Name] = int(f.Offset)
	}
	for _, f := range CLRFields["Player"] {
		player[f.Name] = int(f.Offset)
	}
	s := clrEntry.Statics
	require.Equal(t, ByStatics, clrEntry.LocalPlayer)
	require.Equal(t, main["npc"]-main["player"], s.NPCFromPlayer)
	require.Equal(t, main["projectile"]-main["player"], s.ProjectileFromPlayer)
	require.Equal(t, player["active"], s.PlayerActive)
	require.Equal(t, player["statLife"], s.LifeInPlayer)
	require.Equal(t, [3]uint32{256, 201, 1001}, [3]uint32{s.PlayerLen, s.NPCLen, s.ProjectileLen})
	require.Equal(t, ByAnchor, monoEntry.LocalPlayer)
}

/*
The CLR item fields are the CLR table's, by name, and the mono ones are the
mono constants. Each struct field is mapped to the name the game declares it
under, and a field missing from the map fails rather than going unchecked.
*/
func TestTheCLRItemFieldsAreTheTable(t *testing.T) {
	at := map[string]int{}
	for _, f := range CLRFields["Item"] {
		at[f.Name] = int(f.Offset)
	}
	clr := clrEntry.Item
	byGameName := map[string]int{
		"type": clr.Type, "stack": clr.Stack, "useTime": clr.UseTime, "useAnimation": clr.UseAnim,
		"pick": clr.Pick, "tileBoost": clr.TileBoost, "damage": clr.Damage, "rare": clr.Rare,
		"defense": clr.Defense, "buffType": clr.BuffType, "mana": clr.Mana, "crit": clr.Crit,
		"knockBack": clr.Knockback, "scale": clr.Scale, "shootSpeed": clr.ShootSpeed,
		"fishingPole": clr.FishingPole, "bait": clr.Bait, "prefix": clr.Prefix,
		"autoReuse": clr.AutoReuse, "accessory": clr.Accessory, "favorited": clr.Favorited,
		"consumable": clr.Consumable, "melee": clr.Melee, "magic": clr.Magic,
		"ranged": clr.Ranged, "summon": clr.Summon,
	}
	require.Len(t, byGameName, reflectFieldCount(ItemFields{}), "an ItemFields field is not checked here")
	for name, got := range byGameName {
		want, ok := at[name]
		require.True(t, ok, "Item.%s is not in the CLR table", name)
		require.Equal(t, want, got, "Item.%s", name)
	}
	require.Zero(t, clrEntry.Player.SelectedItemFromLife, "the CLR selected-item field is unmeasured")
	require.Equal(t, SelectedItemOff, monoEntry.Player.SelectedItemFromLife)
	require.Equal(t, ItemType, monoEntry.Item.Type)
	require.Equal(t, ItemSummon, monoEntry.Item.Summon)
}

func reflectFieldCount(v any) int { return reflect.TypeOf(v).NumField() }
