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


def test_copies_that_agree_are_taken_as_the_template():
    """Forty identical copies in a chest are pristine copies; their shared stats are the
    template. The old rule discarded any run that repeated a type, which is what let a
    lone modified copy elsewhere supply the value instead (spec 039)."""
    m = _mem([(2, {ITEM_DAMAGE: 7}) for _ in range(40)])
    assert content.find_item_templates(m, VT)[2]["damage"] == 7


def test_one_modified_copy_does_not_outvote_the_pristine_ones():
    """The bug: an edited item was returned as its own type's template, and spec 038 then
    compared the edit against itself and pruned it."""
    m = _mem([(2, {ITEM_DAMAGE: 7}), (2, {ITEM_DAMAGE: 7}), (2, {ITEM_DAMAGE: 999})])
    assert content.find_item_templates(m, VT)[2]["damage"] == 7


def test_a_prefixed_copy_never_wins_over_an_unprefixed_one():
    """A modifier changes damage and use time, so a prefixed copy is not a template —
    a Muramasa with a damage prefix was being reported as the base item."""
    from terrariabonker.inventory import ITEM_PREFIX

    m = _mem([(2, {ITEM_DAMAGE: 26, ITEM_PREFIX: 65}),
              (2, {ITEM_DAMAGE: 26, ITEM_PREFIX: 65}),
              (2, {ITEM_DAMAGE: 24})])
    assert content.find_item_templates(m, VT)[2]["damage"] == 24, \
        "prefixed copies outvoted the pristine one"


def test_the_prefix_is_not_reported_as_a_stat():
    m = _mem([(2, {ITEM_DAMAGE: 7})])
    assert "prefix" not in content.find_item_templates(m, VT)[2]


def test_excluded_objects_are_not_considered():
    """The player's own items are excluded by address: they are what this program edits,
    so they are the likeliest to be mistaken for a template.

    The excluded copies are deliberately the *majority* here — with the exclusion ignored
    they would carry the vote, so the test fails rather than passing by luck on a tie.
    """
    m = _mem([(2, {ITEM_DAMAGE: 14}),
              (2, {ITEM_DAMAGE: 31}), (2, {ITEM_DAMAGE: 31}), (2, {ITEM_DAMAGE: 31})])
    mine = {BASE + 0x1000 + STRIDE * i for i in (1, 2, 3)}
    got = content.find_item_templates(m, VT, exclude=mine)
    assert got[2]["damage"] == 14, "an excluded object still decided the template"
    loose = content.find_item_templates(m, VT)
    assert loose[2]["damage"] == 31, "premise: without the exclusion they would win"


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
    """The ladder is most-specific first: a boss that is also flagged town is a boss."""
    assert content.npc_kind({"boss": True, "town": True, "damage": 40}) == "Boss"
    assert content.npc_kind({"town": True, "damage": 10}) == "Town NPC"
    assert content.npc_kind({"damage": 14}) == "Monster"


def test_a_damageless_npc_is_a_critter():
    """Bunnies and birds carry 5 life and 0 damage; nothing on the template says critter."""
    assert content.npc_kind({"damage": 0, "life": 5}) == "Critter"
    assert content.npc_kind({}) == "Critter"


def test_a_town_npc_is_never_a_critter_despite_dealing_damage():
    """Town NPCs deal damage, so the critter test must sit below the town test."""
    assert content.npc_kind({"town": True, "damage": 0}) == "Town NPC"


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
