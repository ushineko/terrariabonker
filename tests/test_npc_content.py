"""NPC templates and the Main.npc discovery that finds them (spec 035, phase 2).

NPC stats have to come from the ContentSamples templates, not from Main.npc[]: a live
NPC's stats have already been scaled by the world's difficulty, so a Blue Slime in an
expert world reads 60 life where its template says 25.
"""

import struct

from conftest import FakeMem
from terrariabonker import content, npcs
from terrariabonker.inventory import ARR_DATA_OFF, ARR_LEN_OFF
from terrariabonker.npcs import (NPC_BOSS, NPC_DAMAGE, NPC_DEFENSE, NPC_LIFE_MAX,
                                 NPC_NET_ID, NPC_OBJECT_SIZE, NPC_TOWN, NPC_TYPE)

BASE = 0x30000000
VT = 0x06016490


def _mem(objects, base=BASE, size=0x80000):
    """objects: list of (net_id, {field_off: value}); one NPC-shaped blob each."""
    m = FakeMem(base, size)
    for i, (net_id, fields) in enumerate(objects):
        at = base + 0x1000 + i * NPC_OBJECT_SIZE
        m.write(at, struct.pack("<I", VT))
        m.poke_i32(at + NPC_NET_ID, net_id)
        m.poke_i32(at + NPC_TYPE, max(net_id, 0))
        for off, val in fields.items():
            m.poke_i32(at + off, val)
    return m


def test_finds_one_template_per_net_id():
    m = _mem([(1, {NPC_LIFE_MAX: 25, NPC_DAMAGE: 7, NPC_DEFENSE: 2}),
              (4, {NPC_LIFE_MAX: 2800, NPC_DAMAGE: 15, NPC_DEFENSE: 12, NPC_BOSS: 1}),
              (22, {NPC_LIFE_MAX: 250, NPC_DAMAGE: 10, NPC_TOWN: 1})])
    found = content.find_npc_templates(m, VT)
    assert set(found) == {1, 4, 22}
    assert found[4]["life"] == 2800 and found[4]["boss"] is True
    assert found[22]["town"] is True and found[1]["boss"] is False


def test_variants_keyed_by_negative_net_id_survive():
    """ContentSamples is keyed on netID, and the variants are exactly the negative ones."""
    found = content.find_npc_templates(_mem([(1, {}), (-3, {}), (-10, {})]), VT)
    assert set(found) == {1, -3, -10}


def test_ignores_absurd_net_ids():
    assert set(content.find_npc_templates(_mem([(1, {}), (999999, {})]), VT)) == {1}


def test_live_npcs_do_not_masquerade_as_the_template_table():
    """Main.npc holds many NPCs of the same type; a template table holds one per netID."""
    horde = [(1, {NPC_LIFE_MAX: 60}) for _ in range(40)]      # 40 scaled blue slimes
    assert content.find_npc_templates(_mem(horde), VT) == {}


def test_the_template_table_wins_over_a_smaller_one_to_one_run():
    """A handful of live NPCs can look one-to-one; the real table is bigger."""
    live = [(i, {NPC_LIFE_MAX: 999}) for i in range(1, 4)]
    table = [(i, {NPC_LIFE_MAX: i}) for i in range(1, 40)]
    m = _mem(live, size=0x900000)
    # Beyond CLUSTER_GAP, so this is a separate run rather than the same one.
    for j, (net_id, fields) in enumerate(table):
        at = BASE + 0x500000 + j * NPC_OBJECT_SIZE
        m.write(at, struct.pack("<I", VT))
        m.poke_i32(at + NPC_NET_ID, net_id)
        m.poke_i32(at + NPC_TYPE, net_id)
        for off, val in fields.items():
            m.poke_i32(at + off, val)
    found = content.find_npc_templates(m, VT)
    assert found[1]["life"] == 1, "took the stat from the live NPC, not the template"


# --- Main.npc discovery ------------------------------------------------------

def _array_mem(length, vtables, base=BASE):
    """A fake Main statics block holding one NPC[] whose elements carry `vtables`."""
    m = FakeMem(base, 0x80000)
    arr = base + 0x20000
    m.poke_i32(arr + ARR_LEN_OFF, length)
    for i, vt in enumerate(vtables):
        obj = base + 0x40000 + i * NPC_OBJECT_SIZE
        m.write(obj, struct.pack("<I", vt))
        m.write(arr + ARR_DATA_OFF + i * 4, struct.pack("<I", obj))
    return m, arr


def test_the_npc_vtable_is_the_one_the_elements_agree_on():
    m, arr = _array_mem(npcs.MAX_NPCS + 1, [VT] * 32)
    assert npcs.npc_vtable_of(m, arr) == VT


def test_an_array_whose_elements_disagree_is_rejected():
    """A same-length array of something else must not be mistaken for Main.npc."""
    m, arr = _array_mem(npcs.MAX_NPCS + 1, [VT] + [0xDEADBEEF] * 31)
    assert npcs.npc_vtable_of(m, arr) is None


def test_templates_survive_default_objects_allocated_beside_them():
    """The seven Moss Hornet variants were lost this way.

    They sit next to seven default-state NPC objects, all carrying netID 0. Scoring the
    run as a whole made it 14 objects for 8 distinct netIDs — not one-to-one — and threw
    away all seven real templates. Only the repeated key should drop out.
    """
    hornets = [(-65 + i, {NPC_LIFE_MAX: 45, NPC_DAMAGE: 41}) for i in range(7)]
    filler = [(0, {}) for _ in range(7)]           # default-state objects, all netID 0
    found = content.find_npc_templates(_mem(hornets + filler), VT)
    assert set(found) == {-65, -64, -63, -62, -61, -60, -59}
    assert found[-65]["life"] == 45
