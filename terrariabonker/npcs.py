"""NPCID -> display-name lookup, and the NPC object field offsets (spec 035, phase 2).

Backed by ``data/npcs.json``, extracted from the game's own ``Terraria.exe`` by
``tools/extract_item_names --npcs``: the ``NPCID`` constant fields joined with the embedded
``en-US.NPCs.json`` localization (``NPCName`` plus ``SpecialNPCName``). Unlike items, NPC ids
go negative for some entries, so they are not filtered to positive values.
"""

from __future__ import annotations

import json
import os

# --- NPC object field offsets -----------------------------------------------
# Derived on build 1.4.5.7+24893155 by differencing ContentSamples templates, the same
# way the Item offsets in ``inventory`` were: no IL gives these, because mono decides the
# layout. Each was confirmed against vanilla stats read independently of this project —
# Blue Slime 25/7/2, Zombie 45/14/6, Demon Eye 60/18/2, Eye of Cthulhu 2800/15/12,
# King Slime 2000/40/10, Moon Lord 45000 life, critters 5 life and 0 damage.
NPC_POSITION_X = 0x0C   # Vector2 position, inherited from Entity (Y at +0x10)
NPC_POSITION_Y = 0x10
NPC_WIDTH = 0x34
NPC_HEIGHT = 0x38
NPC_TYPE = 0x140        # base type, 0..696 (no negatives)
NPC_DAMAGE = 0x160
NPC_DEFENSE = 0x164
NPC_LIFE_MAX = 0x170
NPC_BOSS = 0x1CC        # byte bool
NPC_NET_ID = 0x1E8      # type, but negative for variant entries; keys ContentSamples
NPC_TOWN = 0x214        # byte bool (townNPC)
NPC_ACTIVE = 0x1CB      # byte bool, packed next to `boss`. Confirmed two ways: clear in
#                         all 647 ContentSamples templates, and set on exactly the live
#                         Main.npc slots (the one exception was a despawned Squirrel that
#                         had left its type behind).
NPC_WHO_AMI = 0x08      # the NPC's own index in Main.npc
NPC_VELOCITY_X = 0x14   # Vector2 velocity (Y at +0x18)
NPC_OLD_POSITION_X = 0x1C

# Copying a ContentSamples template over a slot is how an NPC is spawned (the same trick
# `give_item` uses for items, one level up), so the copy must skip the object header, the
# entity's own position, and every field holding a reference — those are 0x044..0x06F, and
# handing a slot the template's arrays would make the two share them. What is left is two
# pointer-free spans covering direction/size and the whole stat block.
NPC_COPY_SPANS = ((0x2C, 0x44), (0x70, 0x298))

NPC_OBJECT_SIZE = 0x298  # stride between template objects; also the read span
MAX_NPC_TYPE = 2000      # sanity bound on a plausible NPCID (the game uses up to ~700)

# Main.npc within Main's static-data block, the same kind of constant as
# ``locate.MAIN_PLAYER_OFF`` and ``recipes.MAIN_RECIPE_OFF``, pinned to build
# 1.4.5.7+24893155. Validated on use rather than trusted: the array must be
# ``maxNPCs + 1`` long, and a wrong offset is far more likely to miss that than to hit it.
MAIN_NPC_OFF = 0x9B0
MAX_NPCS = 200

_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "npcs.json")

try:
    with open(_PATH) as _f:
        _NAMES: dict[int, str] = {int(k): v for k, v in json.load(_f).items()}
except (OSError, ValueError):
    _NAMES = {}


def name(npc_id: int) -> str:
    """Display name for an NPCID, or '' if unknown."""
    return _NAMES.get(npc_id, "")


def label(npc_id: int) -> str:
    """A never-empty label: 'Blue Slime' or '#123' for unknown ids."""
    return _NAMES.get(npc_id) or f"#{npc_id}"


def all_names() -> dict[int, str]:
    return dict(_NAMES)


def count() -> int:
    return len(_NAMES)


def find_npc_array(mem) -> int | None:
    """Address of the ``Main.npc`` array object, or None.

    Tries the pinned offset first and checks the length; if that fails — a rebuild moved
    Main's statics — falls back to scanning the static block for the only array of the
    right length whose elements share a vtable. The fallback is what keeps this from
    becoming another constant that silently rots.
    """
    from terrariabonker.inventory import ARR_LEN_OFF
    from terrariabonker.locate import main_static_base

    base = main_static_base(mem)
    if base is None:
        return None
    want = MAX_NPCS + 1

    def length_of(ptr):
        if not ptr or not (0x10000 < ptr < 0xFFFFFFF0):
            return None
        return mem.read_i32(ptr + ARR_LEN_OFF)

    arr = mem.read_u32(base + MAIN_NPC_OFF)
    if length_of(arr) == want:
        return arr

    blob = mem.read(base, 0x4000)
    for off in range(0, len(blob) - 4, 4):
        cand = int.from_bytes(blob[off:off + 4], "little")
        if length_of(cand) != want:
            continue
        if npc_vtable_of(mem, cand) is not None:
            return cand
    return None


def npc_vtable_of(mem, arr: int) -> int | None:
    """The vtable shared by the elements of an NPC array, or None if they do not share one.

    Terraria allocates every ``Main.npc`` slot at world load, so this does not depend on
    anything being alive; requiring agreement across many elements is what rejects an
    unrelated array of the same length.
    """
    from terrariabonker.inventory import ARR_DATA_OFF

    first = mem.read_u32(arr + ARR_DATA_OFF)
    if not first:
        return None
    vt = mem.read_u32(first)
    if not vt:
        return None
    agree = 0
    for i in range(min(MAX_NPCS, 32)):
        elem = mem.read_u32(arr + ARR_DATA_OFF + i * 4)
        if elem and mem.read_u32(elem) == vt:
            agree += 1
    return vt if agree >= 24 else None


def find_npc_vtable(mem) -> int | None:
    """The shared mono vtable of NPC objects, the entry point for the template scan."""
    arr = find_npc_array(mem)
    return npc_vtable_of(mem, arr) if arr else None
