"""Finding the game's item templates and classifying them (spec 035, phase 1).

Stats are assigned by Item.SetDefaults at runtime, so the only authority is the template
objects in memory. ContentSamples.ItemsByType is a Dictionary<int, Item>, and walking a
mono dictionary would depend on the *runtime's* layout rather than Terraria's — a failure
the build ledger would not catch. Instead the templates are found by their shape: a run of
Item objects holding one object per type.
"""

import struct

from terrariabonker import content
from terrariabonker.inventory import (ITEM_ACCESSORY, ITEM_DAMAGE, ITEM_HEAD_SLOT,
                                      ITEM_TYPE)

BASE = 0x30000000
VT = 0xABCD1234
STRIDE = 0x200


def _mem(objects, base=BASE):
    """objects: list of (type, {field_off: value}); one Item-shaped blob each."""
    from conftest import FakeMem
    m = FakeMem(base, 0x40000)
    for i, (itype, fields) in enumerate(objects):
        at = base + 0x1000 + i * STRIDE
        m.write(at, struct.pack("<I", VT))
        m.poke_i32(at + ITEM_TYPE, itype)
        m.poke_i32(at + ITEM_HEAD_SLOT, -1)          # default: not armour
        for off, val in fields.items():
            m.poke_i32(at + off, val)
    return m


def test_finds_one_template_per_type():
    m = _mem([(2, {}), (3507, {ITEM_DAMAGE: 5}), (54, {ITEM_ACCESSORY: 1})])
    found = content.find_item_templates(m, VT)
    assert set(found) == {2, 3507, 54}
    assert found[3507]["damage"] == 5


def test_ignores_objects_that_are_not_items():
    m = _mem([(2, {})])
    m.write(BASE + 0x8000, struct.pack("<I", 0xDEADBEEF))     # some other class
    assert set(content.find_item_templates(m, VT)) == {2}


def test_ignores_absurd_type_values():
    m = _mem([(2, {}), (999999, {})])
    assert set(content.find_item_templates(m, VT)) == {2}


def test_a_repeated_type_run_is_not_mistaken_for_the_template_table():
    """Live items repeat types heavily; a template table holds one object per type."""
    chest = [(2, {ITEM_DAMAGE: 777}) for _ in range(40)]      # 40 copies of one type
    m = _mem(chest)
    # the run is 40 objects for 1 type, far from one-to-one, so it is not a template table
    assert content.find_item_templates(m, VT) == {}


def test_the_larger_table_wins_when_a_type_appears_in_two(monkeypatch):
    """A chest of distinct items can look one-to-one; the real table is bigger.

    The runs must be further apart than CLUSTER_GAP to be separate runs at all, which is
    the knob that decides what counts as one table.
    """
    monkeypatch.setattr(content, "CLUSTER_GAP", 0x4000)
    m = _mem([(2, {ITEM_DAMAGE: 111})])                        # a lone "chest" object
    far = BASE + 0x18000                                       # a separate run
    for i, itype in enumerate(range(2, 40)):
        at = far + i * STRIDE
        m.write(at, struct.pack("<I", VT))
        m.poke_i32(at + ITEM_TYPE, itype)
        m.poke_i32(at + ITEM_HEAD_SLOT, -1)
        m.poke_i32(at + ITEM_DAMAGE, 1)
    found = content.find_item_templates(m, VT)
    assert found[2]["damage"] == 1, "stats should come from the bigger table"


# --- classification ---------------------------------------------------------

def test_kind_accessory_beats_damage():
    assert content.item_kind({"accessory": True, "damage": 20}) == "Accessory"


def test_kind_armor_covers_vanity_with_no_defense():
    assert content.item_kind({"head_slot": 98, "defense": 0}) == "Armor"


def test_kind_potion():
    assert content.item_kind({"heal_life": 100}) == "Potion"
    assert content.item_kind({"heal_mana": 100}) == "Potion"


def test_kind_tool_beats_weapon():
    assert content.item_kind({"pick": 55, "damage": 4}) == "Tool"


def test_kind_splits_weapons_by_damage_class():
    assert content.item_kind({"damage": 10}) == "Weapon"
    assert content.item_kind({"damage": 10, "ranged": True}) == "Ranged"
    assert content.item_kind({"damage": 10, "magic": True}) == "Magic"
    assert content.item_kind({"damage": 10, "summon": True}) == "Summon"


def test_kind_block_then_material():
    assert content.item_kind({"create_tile": 0}) == "Block"
    assert content.item_kind({"create_tile": -1}) == "Material"


def test_npc_kind():
    assert content.npc_kind({"boss": True, "town": True}) == "Boss"
    assert content.npc_kind({"town": True}) == "Town NPC"
    assert content.npc_kind({}) == "Monster"


def test_wiki_url_uses_the_official_wiki_and_underscores():
    assert content.wiki_url("Hermes Boots") == "https://terraria.wiki.gg/wiki/Hermes_Boots"
    assert content.wiki_url("Zenith").endswith("/Zenith")


# --- the shipped data itself -----------------------------------------------

def test_no_unresolved_localization_references_in_names():
    """Localization values reference each other as {$Category.Key}. Resolving only
    CommonItemTooltip left NPC names like "{$NPCName.DD2GoblinT1}" showing raw in the UI."""
    from terrariabonker import names, npcs
    bad_items = [v for v in names.all_names().values() if "{$" in v]
    bad_npcs = [v for v in npcs.all_names().values() if "{$" in v]
    assert bad_items == [], bad_items[:5]
    assert bad_npcs == [], bad_npcs[:5]


def test_tooltips_are_almost_entirely_resolved():
    """A handful reference keys that do not exist in en-US at all; those are left visible
    rather than blanked, but the count must stay tiny."""
    from terrariabonker import names
    unresolved = [t for t in (names.tooltip(i) for i in names.all_names()) if "{$" in t]
    assert len(unresolved) <= 10, unresolved[:5]


def test_known_npc_names_resolved():
    from terrariabonker import npcs
    assert npcs.label(556) == "Etherian Goblin Bomber"
    assert npcs.label(695) == "Cattiva"
    assert npcs.label(4) == "Eye of Cthulhu"


def test_a_summon_staff_is_a_weapon_not_a_potion():
    """Summon staffs grant their minion as a buff, so a buff check placed before the
    damage check silently reclassified 28 of them as potions."""
    assert content.item_kind({"damage": 30, "summon": True, "buff_type": 20}) == "Summon"


def test_buff_potions_count_as_potions():
    """They carry no healLife/healMana, which is why the Potion filter first showed only
    restoratives (16 of them, instead of 264)."""
    assert content.item_kind({"buff_type": 5, "damage": -1}) == "Potion"
