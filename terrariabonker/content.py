"""Every item's default stats, read from the game's own template objects.

Stats like damage, defense and rarity are not in `Terraria.exe` in any readable form —
they are assigned by `Item.SetDefaults` at runtime. The authority is `ContentSamples`,
which holds one fully-populated `Item` per type.

`ContentSamples.ItemsByType` is a `Dictionary<int, Item>`, and walking a mono dictionary
means depending on the *runtime's* field layout rather than Terraria's — a failure mode the
build ledger would not catch cleanly. So this finds the templates a different way: scan the
writable regions for objects carrying the `Item` vtable (the scan `_template_block` already
does for one type), and pick out the template table by its shape.

The table gives itself away by being **one object per type**. Live items — inventories,
chests, dropped items — repeat types heavily, so a run of addresses holding roughly as many
objects as distinct types is a template table. Measured on a live game: 169,637 Item-shaped
objects in 463 ms, of which the one-to-one runs cover 6,162 distinct types, i.e. everything.
"""

from __future__ import annotations

from collections import Counter

import numpy as np

from terrariabonker.inventory import (ITEM_ACCESSORY, ITEM_AUTOREUSE, ITEM_BODY_SLOT,
                                      ITEM_BUFF_TYPE, ITEM_DAMAGE,
                                      ITEM_DEFENSE, ITEM_HEAD_SLOT, ITEM_HEAL_LIFE,
                                      ITEM_HEAL_MANA, ITEM_LEG_SLOT, ITEM_MAGIC, ITEM_MELEE,
                                      ITEM_PICK, ITEM_PREFIX, ITEM_RANGED, ITEM_RARE,
                                      ITEM_SUMMON,
                                      ITEM_TILEBOOST, ITEM_TYPE, ITEM_USE_ANIM,
                                      ITEM_USE_TIME)
from terrariabonker.npcs import (MAX_NPC_TYPE, NPC_BOSS, NPC_COLOR, NPC_DAMAGE,
                                 NPC_DEFENSE, NPC_HEIGHT, NPC_LIFE_MAX, NPC_NET_ID,
                                 NPC_OBJECT_SIZE, NPC_TOWN, NPC_TYPE, NPC_WIDTH)
from terrariabonker.recipes import ITEM_CREATE_TILE

# Addresses further apart than this start a new run. Chosen from live data: it merges the
# template table's chunks without swallowing the surrounding live-item heap.
CLUSTER_GAP = 0x400000
MAX_TYPE = 7000                 # sanity bound on a plausible ItemID


def _read_stats(buf: bytes, off: int) -> dict:
    def i32(field):
        return int.from_bytes(buf[off + field: off + field + 4], "little", signed=True)

    return {
        "type": i32(ITEM_TYPE),
        "damage": i32(ITEM_DAMAGE),
        "defense": i32(ITEM_DEFENSE),
        "rare": i32(ITEM_RARE),
        "pick": i32(ITEM_PICK),
        "use_time": i32(ITEM_USE_TIME),
        # Needed to tell a real edit from an item's own defaults (spec 038).
        "use_anim": i32(ITEM_USE_ANIM),
        "tile_boost": i32(ITEM_TILEBOOST),
        "auto_reuse": int(buf[off + ITEM_AUTOREUSE]),
        "create_tile": i32(ITEM_CREATE_TILE),
        "buff_type": i32(ITEM_BUFF_TYPE),
        "heal_life": i32(ITEM_HEAL_LIFE),
        "heal_mana": i32(ITEM_HEAL_MANA),
        "head_slot": i32(ITEM_HEAD_SLOT),
        "body_slot": i32(ITEM_BODY_SLOT),
        "leg_slot": i32(ITEM_LEG_SLOT),
        # Kept only to tell a modified copy from a pristine one; stripped before use.
        "prefix": int(buf[off + ITEM_PREFIX]),
        "accessory": bool(buf[off + ITEM_ACCESSORY]),
        "melee": bool(buf[off + ITEM_MELEE]),
        "ranged": bool(buf[off + ITEM_RANGED]),
        "magic": bool(buf[off + ITEM_MAGIC]),
        "summon": bool(buf[off + ITEM_SUMMON]),
    }


def _scan_objects(mem, vtable: int, span: int, read, accept) -> list[tuple[int, dict]]:
    """Every object carrying ``vtable``, as ``(address, stats)``, address-ordered.

    ``read(buf, off)`` turns one object into a stats dict; ``accept(stats)`` rejects the
    false positives a bare vtable match always produces (a stale pointer in the middle of
    some other structure reads as an object with an absurd type).
    """
    found: list[tuple[int, dict]] = []
    for start, end in mem.regions():
        buf = mem.read(start, end - start)
        n = len(buf) // 4
        if n < 4:
            continue
        arr = np.frombuffer(buf[: n * 4], dtype=np.uint32)
        for idx in np.where(arr == vtable)[0].tolist():
            off = idx * 4
            if off + span > len(buf):
                continue
            stats = read(buf, off)
            if accept(stats):
                found.append((start + off, stats))
    found.sort(key=lambda p: p[0])
    return found


def _template_table(found: list[tuple[int, dict]], key: str) -> dict[int, dict]:
    """Pick the template table out of every object of a class, keyed by ``key``.

    The table gives itself away by being one object per key: live objects — inventories,
    chests, the NPC slots — repeat their keys heavily, templates never do. Addresses are
    clustered into runs, and from each run we keep the keys that appear in it **exactly
    once**. Bigger runs are consulted first, so where a key appears in more than one run it
    is taken from the real table rather than from a chest that happens to look one-to-one.

    Judging keys rather than whole runs matters. Scoring a run as a whole and discarding it
    if it was not near enough to one-to-one lost real templates to whatever happened to be
    allocated beside them: the seven Moss Hornet variants sit next to seven default-state
    NPC objects, which made their run 14 objects for 8 distinct netIDs and threw away all
    seven. Per-key, the duplicated key drops out and its neighbours survive.
    """
    if not found:
        return {}
    runs: list[list[tuple[int, dict]]] = [[found[0]]]
    for addr, stats in found[1:]:
        if addr - runs[-1][-1][0] > CLUSTER_GAP:
            runs.append([])
        runs[-1].append((addr, stats))

    runs.sort(key=len, reverse=True)
    out: dict[int, dict] = {}
    for run in runs:
        seen = Counter(s[key] for _a, s in run)
        for _addr, stats in run:
            if seen[stats[key]] == 1:
                out.setdefault(stats[key], stats)
    return out


def _consensus(candidates: list[dict]) -> dict | None:
    """The stats a *pristine* copy of an item has (spec 039).

    Two rules, in order. A modifier changes damage and use time, so a prefixed copy is not
    a template: where any copy is unprefixed, only those are considered. Then take the
    most common stat tuple — a pristine template agrees with every other pristine copy of
    its type, while an edited one stands alone.

    Choosing by position instead let a live, modified item be returned as the template. It
    reported the maintainer's own edits as Boomstick's base stats, and spec 038 then used
    those as the baseline for deciding which saved edits were redundant, destroying eight
    of them.
    """
    clean = [c for c in candidates if not c.get("prefix")] or candidates
    if len(clean) == 1:
        return clean[0]
    keys = tuple(k for k in clean[0] if k != "prefix")
    tally = Counter(tuple(c[k] for k in keys) for c in clean)
    winner = tally.most_common(1)[0][0]
    for c in clean:
        if tuple(c[k] for k in keys) == winner:
            return c
    return clean[0]


def find_item_templates(mem, vtable: int, exclude=()) -> dict[int, dict]:
    """``{type: stats}`` for every item the game has a template for.

    ``exclude`` is the addresses of objects known to be the player's own — the ones this
    program edits, and so the likeliest to be mistaken for a template.
    """
    span = max(ITEM_TYPE, ITEM_DAMAGE, ITEM_DEFENSE, ITEM_RARE, ITEM_PICK, ITEM_USE_TIME,
               ITEM_CREATE_TILE, ITEM_ACCESSORY, ITEM_SUMMON, ITEM_LEG_SLOT,
               ITEM_HEAL_MANA, ITEM_BUFF_TYPE, ITEM_USE_ANIM, ITEM_TILEBOOST,
               ITEM_AUTOREUSE, ITEM_PREFIX) + 4
    skip = set(exclude or ())
    found = _scan_objects(mem, vtable, span, _read_stats,
                          lambda s: 0 < s["type"] < MAX_TYPE)
    by_type: dict[int, list[dict]] = {}
    for addr, stats in found:
        if addr in skip:
            continue
        by_type.setdefault(stats["type"], []).append(stats)
    out = {}
    for t, cands in by_type.items():
        got = _consensus(cands)
        if got is not None:
            out[t] = {k: v for k, v in got.items() if k != "prefix"}
    return out


def _read_npc_stats(buf: bytes, off: int) -> dict:
    def i32(field):
        return int.from_bytes(buf[off + field: off + field + 4], "little", signed=True)

    return {
        "type": i32(NPC_TYPE),
        "net_id": i32(NPC_NET_ID),
        "life": i32(NPC_LIFE_MAX),
        "damage": i32(NPC_DAMAGE),
        "defense": i32(NPC_DEFENSE),
        "width": i32(NPC_WIDTH),
        "height": i32(NPC_HEIGHT),
        "boss": bool(buf[off + NPC_BOSS]),
        "town": bool(buf[off + NPC_TOWN]),
        # The tint the game paints a neutral sheet with; (0,0,0,0) means "no tint".
        "color": list(buf[off + NPC_COLOR: off + NPC_COLOR + 4]),
    }


def find_npc_templates(mem, vtable: int, exclude=()) -> dict[int, dict]:
    """``{net_id: stats}`` for every NPC the game has a template for.

    Keyed on ``netID`` rather than ``type`` because ContentSamples is: the variant entries
    (the coloured slimes and so on) share a type and are told apart only by a negative
    netID, and those are exactly the names ``data/npcs.json`` carries.

    Reading the templates rather than ``Main.npc[]`` is not a detail — a live NPC's stats
    have been scaled by the world's difficulty, so a Blue Slime in an expert world reads
    60 life where its template says 25.
    """
    skip = set(exclude or ())
    found = _scan_objects(mem, vtable, NPC_OBJECT_SIZE, _read_npc_stats,
                          lambda s: -MAX_NPC_TYPE < s["net_id"] < MAX_NPC_TYPE
                          and 0 <= s["type"] < MAX_NPC_TYPE)
    # The NPCs in the world have had their stats scaled by the world's difficulty, so they
    # are not templates however they happen to be laid out (spec 039).
    found = [(a, st) for a, st in found if a not in skip]
    return _template_table(found, "net_id")


def item_kind(stats: dict) -> str:
    """A one-word category for the compendium, from the fields the project already reads.

    Ordered most-specific first: an accessory that also has damage is still an accessory,
    and a pickaxe that deals damage is still a tool.
    """
    if stats.get("accessory"):
        return "Accessory"
    if max(stats.get("head_slot", -1), stats.get("body_slot", -1),
           stats.get("leg_slot", -1)) >= 0:
        return "Armor"          # includes vanity: a slot with no defense is still worn
    if stats.get("pick", 0) > 0:
        return "Tool"
    if stats.get("damage", 0) > 0:
        # Before the buff check: a summon staff grants its minion as a buff, and is a
        # weapon, not a potion.
        if stats.get("summon"):
            return "Summon"
        if stats.get("magic"):
            return "Magic"
        if stats.get("ranged"):
            return "Ranged"
        return "Weapon"
    if (stats.get("heal_life", 0) > 0 or stats.get("heal_mana", 0) > 0
            or stats.get("buff_type", 0) > 0):
        # Buff potions carry no healLife/healMana at all, which is why filtering on those
        # alone showed only restoratives. Food grants a buff too and lands here.
        return "Potion"
    if stats.get("defense", 0) > 0:
        return "Armor"
    if stats.get("create_tile", -1) >= 0:
        return "Block"
    return "Material"


def npc_kind(stats: dict) -> str:
    """Boss / town NPC / critter / monster, from the NPC template's own fields.

    ``boss`` and ``townNPC`` are real flags on the object. "Critter" is not — nothing on
    the template says "critter" in a way this project could confirm, so it is defined by
    what is observable: an NPC that deals no damage. That puts the bunnies and birds
    (5 life, 0 damage) where a player expects them, and misfiles the Target Dummy, which
    is an acceptable price for not inventing a flag.
    """
    if stats.get("boss"):
        return "Boss"
    if stats.get("town"):
        return "Town NPC"
    if stats.get("damage", 0) <= 0:
        return "Critter"
    return "Monster"


def wiki_url(name: str) -> str:
    """The official wiki article for a display name.

    Opened in the user's browser rather than fetched: the app makes no network requests.
    Article titles use underscores; a name that redirects or misses lands on the wiki's own
    search, which is an acceptable outcome for the handful that differ.
    """
    slug = "_".join(name.split())
    return "https://terraria.wiki.gg/wiki/" + slug
