package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/content"
	"github.com/ushineko/terrariabonker/internal/memtest"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/selling"
	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/sprites"
)

/*
Selling, the fishing gear and the NPC catalog under .NET Framework (spec 052).

Every number planted here is a literal from the measurement, not read from
internal/layout -- a fixture planted through layout's numbers and read back
through them proves only that the numbers equal themselves. cmd/clrfields
against 1.4.5.8 on .NET Framework 4.8.1 (docs/clr-fields-1.4.5.8.txt):

  - Item: fishingPole 0x048, bait 0x04C, type 0x050, stack 0x064, value 0x0D8,
    favorited 0x10C. A template is copied from +0x018.
  - Player: bank 0x0E0, statLife 0x470. Chest: item 0x004.
  - An szarray keeps its length at +4 and its elements from +8.
  - Main's statics, from Main.player's slot: npc -0x54, tile -0x68, worldName
    -0x280, npcFrameCount +0xA0.
  - NPC: type 0x0F4, damage 0x108, defense 0x10C, lifeMax 0x120, netID 0x14C,
    color 0x208.
*/

const (
	csRuntime = "netfx-4.8.9345.0"
	csBase    = 0x10000000
	csSize    = 0x80000

	csTemplates = csBase + 0x12000 // item templates, below every inventory
	csLiveArr   = csBase + 0x20000
	csLiveItems = csLiveArr + 0x400
	csSnapArr   = csBase + 0x26000
	csSnapItems = csSnapArr + 0x400
	csChest     = csBase + 0x30000
	csBankArr   = csBase + 0x30100
	csBankItems = csBase + 0x31000
	csBankStep  = 0x200

	csNPCObjects   = csBase + 0x40000 // what Main.npc holds
	csNPCTemplates = csBase + 0x60000
	csFrameCounts  = csBase + 0x70000
	csNPCMT        = 0x084FE6F0
)

// csGame is a CLR world with room for inventories, a bank and NPCs, and the
// service over it.
func csGame(t *testing.T) (memtest.CLRWorld, *service.Service) {
	t.Helper()
	w := memtest.PlantCLRWorld(csBase)
	big := memtest.New(w.Base, csSize)
	copy(big.Buf, w.Mem.Buf)
	w.Mem = big
	return w, service.New(&execMem{w.Mem}, -1).WithRuntime(csRuntime)
}

// csInventories gives the live player and the snapshot the same items, with an
// empty Item object in every other slot, as the game keeps them.
func csInventories(w memtest.CLRWorld, items []memtest.CLRItem) {
	held := map[int]bool{}
	for _, it := range items {
		held[it.Slot] = true
	}
	all := append([]memtest.CLRItem{}, items...)
	for slot := range memtest.CLRInventorySlots {
		if !held[slot] {
			all = append(all, memtest.CLRItem{Slot: slot})
		}
	}
	w.Mem.PlantCLRInventory(w.Live+0x470, csLiveArr, csLiveItems, all)
	w.Mem.PlantCLRInventory(w.Snap+0x470, csSnapArr, csSnapItems, all)
}

// csItem is the address of a planted inventory item.
func csItem(items uint32, slot int) uint32 {
	return items + uint32(slot)*memtest.CLRItemSize //nolint:gosec // a slot index
}

// csBank plants the live player's Piggy Bank: forty empty Item objects.
func csBank(w memtest.CLRWorld) {
	w.Mem.PokeBytes(w.Live+0x0E0, u32(csChest))
	w.Mem.PokeBytes(csChest+0x04, u32(csBankArr))
	elems := make([]uint32, 40)
	for i := range elems {
		elems[i] = csBankItems + uint32(i)*csBankStep //nolint:gosec // a slot index
		w.Mem.PokeI32(elems[i], int32(memtest.CLRItemMT))
	}
	w.Mem.PlantCLRArray(csBankArr, 40, elems)
}

// csCoinTemplates plants the game's master copy of each coin, worth what the
// live game reads them as: 5, 500, 50000 and 5000000.
func csCoinTemplates(w memtest.CLRWorld) {
	for i, value := range []int32{5, 500, 50000, 5000000} {
		w.Mem.PlantCLRTemplate(csTemplates+uint32(i)*0x400, int32(71+i), //nolint:gosec // a small index
			map[uint32]int32{0x064: 1, 0x0D8: value})
	}
}

// csSellable is a whitelisted stack worth selling, a favorited one beside it,
// and a carried Piggy Bank, which makes the bank reachable anywhere.
func csSellable(w memtest.CLRWorld) {
	csInventories(w, []memtest.CLRItem{
		{Slot: 12, Type: 9, Stack: 99},
		{Slot: 13, Type: 9, Stack: 40, Favorited: true},
		{Slot: 20, Type: selling.PiggyBankItem, Stack: 1},
	})
	for _, items := range []uint32{csLiveItems, csSnapItems} {
		w.Mem.PokeI32(csItem(items, 12)+0x0D8, 500)
		w.Mem.PokeI32(csItem(items, 13)+0x0D8, 500)
	}
	csBank(w)
	csCoinTemplates(w)
}

func csI32(t *testing.T, w memtest.CLRWorld, addr uint32) int32 {
	t.Helper()
	v, ok := w.Mem.ReadI32(addr)
	require.True(t, ok)
	return v
}

/*
A sale under the CLR pays into the Piggy Bank and then takes the stack.

Ninety-nine of an item whose value reads 500 sell for a silver each. The coins
arrive as a copy of the game's own silver-coin template, so the bank slot reads
the template's value as well as its type; the stack goes from every copy of the
live character; and the favorited stack of the same item stays.
*/
func TestSellingUnderTheCLR(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	w, svc := csGame(t)
	csSellable(w)

	got, err := svc.SellTick(false)
	require.NoError(t, err)
	require.Equal(t, []service.Sold{{Slot: 12, Type: 9, Name: "Wood", Stack: 99, Copper: 9900}}, got["sold"])
	require.Equal(t, []service.Skipped{{Slot: 13, Type: 9, Why: "favorited"}}, got["skipped"])
	require.Equal(t, "bank", got["destination"])
	require.Equal(t, []selling.Coins{{Type: 72, Stack: 99}}, got["paid"])
	require.EqualValues(t, 0, got["unpaid"])

	require.EqualValues(t, 72, csI32(t, w, csBankItems+0x050), "the bank's first slot is not silver")
	require.EqualValues(t, 99, csI32(t, w, csBankItems+0x064))
	require.EqualValues(t, 500, csI32(t, w, csBankItems+0x0D8), "the coin is not a copy of its template")
	require.EqualValues(t, memtest.CLRItemMT, csI32(t, w, csBankItems), "the copy ran over the object header")
	require.EqualValues(t, 0, csI32(t, w, csBankItems+csBankStep+0x050), "a second slot was written")

	for name, items := range map[string]uint32{"live": csLiveItems, "snapshot": csSnapItems} {
		require.EqualValuesf(t, 0, csI32(t, w, csItem(items, 12)+0x050), "the %s stack was not taken", name)
		require.EqualValuesf(t, 0, csI32(t, w, csItem(items, 12)+0x064), "the %s stack was not taken", name)
		require.EqualValuesf(t, 9, csI32(t, w, csItem(items, 13)+0x050), "the %s favorited stack was sold", name)
		require.EqualValuesf(t, 40, csI32(t, w, csItem(items, 13)+0x064), "the %s favorited stack was sold", name)
	}
}

// A dry run under the CLR prices the same sale and writes nothing.
func TestADryRunUnderTheCLRWritesNothing(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	w, svc := csGame(t)
	csSellable(w)
	before := w.Mem.Hex()

	got, err := svc.SellTick(true)
	require.NoError(t, err)
	require.Len(t, got["sold"], 1)
	require.EqualValues(t, 9900, got["copper"])
	require.Equal(t, "bank", got["destination"])
	require.Equal(t, before, w.Mem.Hex(), "a dry run wrote something")
}

/*
A chest that is not the bank's forty slots is not paid into: the coins go to the
inventory instead, where the game's own sell path would put them.
*/
func TestACLRChestOfTheWrongLengthIsNotTheBank(t *testing.T) {
	atHome(t)
	require.NoError(t, profile.SetSellWhitelist(9, true))
	w, svc := csGame(t)
	csSellable(w)
	w.Mem.PokeI32(csBankArr+0x04, 39)

	got, err := svc.SellTick(true)
	require.NoError(t, err)
	require.Equal(t, "inventory", got["destination"])
}

/*
csWorld plants the loaded world the CLR way: Main.worldName's string and a 2x2
Main.tile whose first tile is of the given type, both reached from Main.player's
slot.
*/
func csWorld(w memtest.CLRWorld, name string, tileType byte) {
	const nameAt, tileArr, tileObjs = csBase + 0x15000, csBase + 0x16000, csBase + 0x16800
	w.Mem.PlantCLRString(nameAt, name)
	w.Mem.PokeBytes(w.Slot-0x280, u32(nameAt))
	w.Mem.PokeBytes(w.Slot-0x68, u32(tileArr))
	w.Mem.PokeI32(tileArr+0x04, 4)
	w.Mem.PokeI32(tileArr+0x08, 2)
	w.Mem.PokeI32(tileArr+0x0C, 2)
	for i := range uint32(4) {
		obj := tileObjs + i*0x18
		w.Mem.PokeBytes(tileArr+0x18+i*4, u32(obj))
		w.Mem.PokeBytes(obj+0x04, []byte{1, 0})
		w.Mem.PokeBytes(obj+0x08, []byte{0x20, 0})
	}
	w.Mem.PokeBytes(tileObjs+0x04, []byte{tileType, 0})
}

/*
Whether a bank is placed is asked again when the world changes, under the CLR as
under mono.

The answer is kept per world, and the world is known by its name -- read through
Main.worldName's slot. Without that the first world's answer would be carried
into every world after it for as long as the service lives.
*/
func TestUnderTheCLRADifferentWorldIsAskedAgain(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, nil)
	csWorld(w, "Royal Brewery", selling.PiggyBankTile)

	world, ok := svc.WorldID()
	require.True(t, ok, "the world was not identified")
	require.Equal(t, service.World{Name: "Royal Brewery", Width: 2, Height: 2}, world)

	got, err := svc.BankReachable(false)
	require.NoError(t, err)
	require.True(t, got.Reachable, "the bank that was planted was not found")
	require.Equal(t, "placed", got.Why)

	csWorld(w, "Somewhere Else", 1)
	got, err = svc.BankReachable(false)
	require.NoError(t, err)
	require.False(t, got.Reachable, "the last world's answer was carried into a new one")
}

/*
The fishing gear under the CLR: bait topped up in every copy of the live
character, and a rod's power raised and put back, each at the CLR's item offsets.
*/
func TestFishingGearUnderTheCLR(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, []memtest.CLRItem{
		{Slot: 8, Type: 2676, Stack: 3},
		{Slot: 9, Type: 2294, Stack: 1},
	})
	for _, items := range []uint32{csLiveItems, csSnapItems} {
		w.Mem.PokeBytes(csItem(items, 8)+0x04C, []byte{50}) // bait power
		w.Mem.PokeBytes(csItem(items, 9)+0x048, []byte{50}) // fishing power
	}

	tick, err := svc.BaitTick(30)
	require.NoError(t, err)
	require.Equal(t, []service.Topped{{Slot: 8, Power: 50, Was: 3, Now: 30}}, tick["topped"])
	for _, items := range []uint32{csLiveItems, csSnapItems} {
		require.EqualValues(t, 30, csI32(t, w, csItem(items, 8)+0x064))
		require.EqualValues(t, 1, csI32(t, w, csItem(items, 9)+0x064), "the rod's stack was topped up")
	}

	raised, err := svc.SetFishingPower(120)
	require.NoError(t, err)
	require.Equal(t, []service.PowerChange{{Slot: 9, Type: 2294, Was: 50, Now: 120}}, raised["changed"])
	// The rod's power is written to the live copy alone, as under mono.
	require.EqualValues(t, 120, csI32(t, w, csItem(csLiveItems, 9)+0x048))
	require.EqualValues(t, 50, csI32(t, w, csItem(csLiveItems, 8)+0x04C), "the bait's power moved")

	done, err := svc.RestoreFishingPower()
	require.NoError(t, err)
	require.Equal(t, []service.Restored{{Slot: 9, Type: 2294, Power: 50}}, done["restored"])
	require.EqualValues(t, 50, csI32(t, w, csItem(csLiveItems, 9)+0x048))
}

/*
The kit under the CLR hands a rod and bait to a player with neither, as copies of
the game's templates, and nothing the second time.
*/
func TestTheFishingKitUnderTheCLR(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, []memtest.CLRItem{{Slot: 0, Type: 757, Stack: 1}})
	w.Mem.PlantCLRTemplate(csTemplates, service.KitRod, map[uint32]int32{0x064: 1, 0x048: 50})
	w.Mem.PlantCLRTemplate(csTemplates+0x400, service.KitBait, map[uint32]int32{0x064: 1, 0x04C: 50})

	gear, err := svc.FishingKit()
	require.NoError(t, err)
	require.Equal(t, map[string]service.Given{
		"rod":  {Type: service.KitRod, Slot: 1},
		"bait": {Type: service.KitBait, Slot: 2, Stack: service.KitBaitStack},
	}, gear.Gave)
	require.Equal(t, []service.RodRow{{Slot: 1, Power: 50}}, gear.Rods)
	require.Equal(t, []service.BaitRow{{Slot: 2, Power: 50, Stack: service.KitBaitStack}}, gear.Baits)
	require.EqualValues(t, service.KitRod, csI32(t, w, csItem(csLiveItems, 1)+0x050))
	require.EqualValues(t, service.KitBaitStack, csI32(t, w, csItem(csLiveItems, 2)+0x064))

	again, err := svc.FishingKit()
	require.NoError(t, err)
	require.Empty(t, again.Gave, "a player with gear was given more")
}

// csNPC plants one NPC object.
func csNPC(w memtest.CLRWorld, at uint32, netID, npcType, life int32) {
	w.Mem.PokeI32(at, csNPCMT)
	w.Mem.PokeI32(at+0x0F4, npcType)
	w.Mem.PokeI32(at+0x14C, netID)
	w.Mem.PokeI32(at+0x120, life)
}

/*
csNPCs plants Main.npc -- 201 objects, the first a live Blue Slime with the life
an expert world scales it to -- the game's templates somewhere else, and the
frame-count table.
*/
func csNPCs(w memtest.CLRWorld) {
	elems := make([]uint32, memtest.CLRNPCSlots)
	for i := range elems {
		elems[i] = csNPCObjects + uint32(i)*0x220 //nolint:gosec // a slot index
		w.Mem.PokeI32(elems[i], csNPCMT)
	}
	w.Mem.PlantCLRArray(w.NPCs, memtest.CLRNPCSlots, elems)
	csNPC(w, elems[0], 1, 1, 60)

	csNPC(w, csNPCTemplates, 1, 1, 25)
	w.Mem.PokeI32(csNPCTemplates+0x108, 7)
	w.Mem.PokeI32(csNPCTemplates+0x10C, 2)
	w.Mem.PokeBytes(csNPCTemplates+0x208, []byte{0, 80, 255, 100})
	csNPC(w, csNPCTemplates+0x300, 4, 4, 2800)
	w.Mem.PokeBytes(csNPCTemplates+0x300+0x1D1, []byte{1})
	csNPC(w, csNPCTemplates+0x600, 17, 17, 250)
	w.Mem.PokeBytes(csNPCTemplates+0x600+0x1D6, []byte{1})

	counts := make([]uint32, 700)
	for i := range counts {
		counts[i] = 1
	}
	counts[1], counts[4], counts[17] = 2, 6, 25
	w.Mem.PlantCLRArray(csFrameCounts, len(counts), counts)
	w.Mem.PokeBytes(w.Slot+0xA0, u32(csFrameCounts))
}

/*
The catalog under the CLR carries the NPCs' stats, read from the game's templates
and not from the NPCs standing in the world.

The live Blue Slime in Main.npc reads 60 life, as an expert world scales it. It
shares a net id with its template, so if it were not left out of the scan the
two would cancel and the Blue Slime would have no stats at all.
*/
func TestTheCatalogUnderTheCLRCarriesNPCStats(t *testing.T) {
	atHome(t)
	w, svc := csGame(t)
	csInventories(w, nil)
	csNPCs(w)

	cat, err := svc.Compendium(true)
	require.NoError(t, err)

	slime := findNPC(t, cat, 1)
	require.Equal(t, "Monster", slime.Kind)
	require.Equal(t, content.NPCStats{Type: 1, NetID: 1, Life: 25, Damage: 7, Defense: 2,
		Color: [4]byte{0, 80, 255, 100}}, slime.Stats)
	require.Equal(t, "Boss", findNPC(t, cat, 4).Kind)
	require.Equal(t, "Town NPC", findNPC(t, cat, 17).Kind)
	require.Equal(t, "NPC", findNPC(t, cat, 2).Kind, "an NPC with no template was given stats")

	frames, tints := sprites.LoadNPCDrawData()
	require.Equal(t, int32(2), frames[1])
	require.Equal(t, int32(6), frames[4])
	require.Equal(t, int32(25), frames[17])
	require.Equal(t, sprites.NPCTint{Type: 1, Color: [4]byte{0, 80, 255, 100}}, tints[1])
	require.NotContains(t, tints, int32(4))
}
