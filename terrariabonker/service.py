"""View-neutral core: every operation the CLI and GUI expose, in one place.

This layer is deliberately toolkit-free (no argparse, no PyQt) so both shells
consume the same operations and data shapes and cannot drift. It assumes it runs
with the privilege needed to read/write game memory (the CLI self-elevates before
constructing a Service; the GUI reaches these operations across a sudo subprocess
boundary via ``gui.client``). ``tests/test_service_neutrality.py`` enforces the
no-toolkit rule structurally.

All operations return plain dataclasses; ``dataclasses.asdict`` gives the JSON the
subprocess boundary carries.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass, field

import numpy as np

from terrariabonker import names
from terrariabonker import version as ver
from terrariabonker.inventory import INVENTORY_SLOTS, ITEM_TYPE, Inventory, Slot
from terrariabonker.locate import (find_localplayer_anchor, find_players, local_player_at,
                                   pick_live, read_block)
from terrariabonker.player import Player
from terrariabonker.proc import Mem, ProcError, find_pid

# Main-inventory slots used for "give" (0-49): skips coin/ammo/equip slots.
GIVE_RANGE = range(0, 50)

# When changing a slot's item, copy the pristine template's field block so the
# new item has real stats (SetDefaults values), not a bare type with zeroed
# damage/useTime. Range starts past the object header/reference pointers and
# covers the stat block; every Item is the same large class, so this stays
# safely inside the object.
ITEM_COPY_LO = 0x1C
ITEM_COPY_HI = 0x140


class ServiceError(RuntimeError):
    """A user-facing failure (game not found, no player, incompatible build)."""


@dataclass(frozen=True)
class PlayerState:
    name: str
    hp: int
    max_hp: int
    mana: int
    max_mana: int


@dataclass(frozen=True)
class ItemSlot:
    slot: int
    type: int
    stack: int
    damage: int
    auto_reuse: int
    use_time: int
    pick: int
    tile_boost: int
    use_anim: int
    rare: int
    defense: int
    prefix: int
    flags: dict = field(default_factory=dict)   # melee/ranged/magic/summon/accessory bools


@dataclass(frozen=True)
class Snapshot:
    pid: int
    version: str | None
    buildid: str | None
    compat_level: str
    compat_msg: str
    copies: int
    player: PlayerState | None
    inventory: list[ItemSlot] = field(default_factory=list)


class Service:
    """Bound to one running game process; all operations go through it."""

    def __init__(self, mem: Mem):
        self.mem = mem
        # Locating is ~99% of a read's cost (a full scan per call), so a long-lived
        # caller (the `serve` worker) keeps the results and re-validates them cheaply.
        # A one-shot CLI run locates once and exits, so it is unaffected.
        self._blocks = None                  # cached find_players() result
        self._anchor = None                  # cached Main.get_LocalPlayer anchor
        self._compat = None                  # build never changes while the process lives
        self._main_base = None               # cached Main static block (see tilemap())
        self._watch = None                   # live _VeinWatch, driven by watch_tick()

    def invalidate(self) -> None:
        """Drop cached locate results; the next call rescans from scratch."""
        self._blocks = None
        self._anchor = None
        self._compat = None
        self._main_base = None

    @classmethod
    def connect(cls) -> "Service":
        """Attach to the running game. Assumes the caller already has root."""
        try:
            return cls(Mem(find_pid()))
        except ProcError as e:
            raise ServiceError(str(e)) from e

    # --- build gate --------------------------------------------------------
    def _build_info(self):
        """(version, buildid, level, msg). Cached only once it reads as a real build.

        The version is scanned out of process memory, and during game startup the CLR's
        own "2.0.50727" can outnumber Terraria's before the game loads its own string.
        Caching that would pin the build at "incompatible" for the life of this Service,
        which wedges every mutating op behind require_compatible() — restore included.
        So an unknown/incompatible reading is re-detected on the next call instead.
        """
        if self._compat is not None:
            return self._compat
        version = ver.detect_version(self.mem)
        buildid = ver.read_buildid(self.mem.exe_path())
        level, msg = ver.compatibility(version, buildid)
        info = (version, buildid, level, msg)
        if level in ("exact", "hotfix"):
            self._compat = info
        return info

    def compatibility(self) -> tuple[str, str]:
        return self._build_info()[2:]

    def require_compatible(self, force: bool = False) -> None:
        """Raise on an incompatible build unless forced (for mutating ops)."""
        level, msg = self.compatibility()
        if level == "incompatible" and not force:
            raise ServiceError(
                f"{msg}. Re-derive offsets (docs/discovery.md) or force to override.")

    # --- locating ----------------------------------------------------------
    def players(self):
        if self._blocks is not None and self._blocks_valid(self._blocks):
            return self._blocks
        blocks = find_players(self.mem)
        if not blocks:
            self._blocks = None
            raise ServiceError("no player found. Load into a world first.")
        self._blocks = blocks
        return blocks

    def _blocks_valid(self, blocks) -> bool:
        """Cheap re-validation of cached addresses (a few reads, no scan).

        The managed heap is GC'd, so a cached ``life_addr`` can stop being a player
        block. Two things must hold: every cached address still reads back as the same
        named player, and the live player (ground truth) is still one of them — the
        latter catches a world reload that moved the player to a new object, which
        would otherwise leave writes landing on dead copies.
        """
        for b in blocks:
            fresh = read_block(self.mem, b.life_addr)
            if fresh is None or fresh.name != b.name:
                return False
        live = self._resolve_live()
        if live is None:
            # No ground truth (anchor gone, or Main.player[myPlayer] is null mid-load):
            # a dead copy can still read back as the same named player, so the cache
            # cannot be confirmed. Fail safe and rescan rather than write to a corpse.
            return False
        return live.life_addr in {b.life_addr for b in blocks}

    def _resolve_live(self):
        """Ground truth through a cached anchor, re-finding it if it stops resolving."""
        if self._anchor is not None:
            blk = local_player_at(self.mem, self._anchor)
            if blk is not None:
                return blk
        self._anchor = find_localplayer_anchor(self.mem)
        if self._anchor is None:
            return None
        return local_player_at(self.mem, self._anchor)

    def _select_live(self, blocks):
        """Pick the live player copy. Ground truth first (Main.player[myPlayer], works
        even while paused); fall back to the activity heuristic, then richest inventory.
        The heuristic's max-inventory fallback is unreliable — a frozen snapshot can
        hold more items than the live player — so ground truth is strongly preferred."""
        live = self._resolve_live()
        if live is not None:
            return live
        live = pick_live(self.mem, blocks)
        if live is None:
            live = max(blocks,
                       key=lambda b: Inventory(self.mem, b.life_addr).nonempty_count())
        return live

    def live_block(self):
        """The live player copy (see _select_live)."""
        return self._select_live(self.players())

    def _all_targets(self):
        """Every player copy as a Player handle (stat writes hit all; snapshots inert)."""
        return [Player(self.mem, b.life_addr) for b in self.players()]

    def _live_inventory(self) -> Inventory:
        return Inventory(self.mem, self.live_block().life_addr)

    def _all_inventories(self) -> list[Inventory]:
        """Every player copy's inventory. **For writing.**

        Writes go to all of them so the live copy is always hit; the inert copies ignore
        what lands on them. Do NOT read from these to decide anything -- the copies are
        not identical, a snapshot holds whatever the slot contained when it was taken,
        and ``invs[0]`` is not necessarily the live one. Read from
        :meth:`_live_inventory`, which is what ``inventory()`` reports to the caller.
        """
        return [Inventory(self.mem, b.life_addr) for b in self.players()]

    def _own_item_addrs(self) -> set:
        """Addresses of the player's own Item objects (spec 039).

        These are the ones this program edits, so they are the likeliest to be mistaken
        for an item's pristine template — and the cheapest contaminant to remove, because
        we know exactly where they are.
        """
        out = set()
        try:
            for inv in self._all_inventories():
                for s in inv.slots():
                    addr = inv._item_addr(s.index)
                    if addr:
                        out.add(addr)
        except (ServiceError, AttributeError):
            pass
        return out

    def _item_vtable(self) -> int | None:
        """The shared mono vtable of Item objects, read from any real item.

        The vtable is the same in every copy, but an inert one can be empty where the
        live one is not, so read the live copy and fall back to the others rather than
        returning None because copy 0 happened to hold nothing.
        """
        for inv in [self._live_inventory()] + self._all_inventories():
            for s in inv.slots():
                if not s.empty:
                    return self.mem.read_u32(s.item_addr)
        return None

    def _template_block(self, item_type: int) -> bytes | None:
        """Field bytes of a pristine Item of this type (from ContentSamples).

        The game keeps a template Item for every type; scan for an Item object
        (vtable match) whose type field equals ``item_type`` and return its stat
        block. Returns None if not found (caller falls back to a bare type set).
        """
        vt = self._item_vtable()
        if vt is None:
            return None
        for start, end in self.mem.regions():
            buf = self.mem.read(start, end - start)
            n = len(buf) // 4
            if n < 1:
                continue
            arr = np.frombuffer(buf[: n * 4], dtype=np.int32)
            for idx in np.where(arr == item_type)[0].tolist():
                off = idx * 4 - ITEM_TYPE
                if off < 0 or off + 4 > len(buf):
                    continue
                if struct.unpack_from("<I", buf, off)[0] != vt:
                    continue
                base = start + off
                block = self.mem.read(base + ITEM_COPY_LO, ITEM_COPY_HI - ITEM_COPY_LO)
                if len(block) == ITEM_COPY_HI - ITEM_COPY_LO:
                    return block
        return None

    def _place_item(self, invs: list[Inventory], slot: int, item_type: int,
                    block: bytes | None) -> None:
        """Set a slot to an item type in every copy, using the template block if
        available so the item has real stats, else a bare type set."""
        for inv in invs:
            addr = inv._item_addr(slot)
            if addr and block:
                self.mem.write(addr + ITEM_COPY_LO, block)
            else:
                inv.set_type(slot, item_type)

    # --- reads -------------------------------------------------------------
    def snapshot(self, with_inventory: bool = True) -> Snapshot:
        try:
            blocks = self.players()          # cached + validated; [] when none loaded
        except ServiceError:
            blocks = []
        version, buildid, level, msg = self._build_info()
        player = inv_slots = None
        inv_slots = []
        if blocks:
            live = self._select_live(blocks)
            player = PlayerState(live.name, live.stat_life, live.stat_life_max,
                                 live.stat_mana, live.stat_mana_max)
            if with_inventory:
                inv_slots = [self._to_slot(s)
                             for s in Inventory(self.mem, live.life_addr).slots()]
        return Snapshot(self.mem.pid, version, buildid, level, msg,
                        len(blocks), player, inv_slots)

    @staticmethod
    def _to_slot(s: Slot) -> ItemSlot:
        return ItemSlot(s.index, s.type, s.stack, s.damage, s.auto_reuse,
                        s.use_time, s.pick, s.tile_boost, s.use_anim, s.rare,
                        s.defense, s.prefix, s.flags)

    def inventory(self) -> list[ItemSlot]:
        return [self._to_slot(s) for s in self._live_inventory().slots()]

    # --- stat mutations (applied to all copies; live one takes effect) ------
    def set_hp(self, value) -> None:
        for p in self._all_targets():
            p.set_life(p.stat_life_max if value == "max" else int(value))

    def set_mana(self, value) -> None:
        for p in self._all_targets():
            p.set_mana(p.stat_mana_max if value == "max" else int(value))

    def set_max_hp(self, value: int) -> None:
        for p in self._all_targets():
            p.set_max_life(int(value))

    def set_max_mana(self, value: int) -> None:
        for p in self._all_targets():
            p.set_max_mana(int(value))

    # --- inventory mutations (applied to every copy) -----------------------
    def set_stack(self, slot: int, value: int) -> None:
        for inv in self._all_inventories():
            inv.set_stack(slot, value)

    def set_item(self, slot: int, item_type: int, *, stack=None, damage=None,
                 auto_reuse=None, use_time=None, use_anim=None, pick=None,
                 tile_boost=None, defense=None, prefix=None,
                 expect_type: int | None = None) -> None:
        invs = self._all_inventories()
        # Read the LIVE copy, not invs[0]. Writes go to every copy because the inert ones
        # ignore them, but a *read* has to come from the same copy the caller was looking
        # at: `inventory()` reports the live one, so comparing against another copy
        # compares against something the user never saw. The copies are not identical --
        # a snapshot holds whatever was in the slot when it was taken, which is how
        # editing the last hotbar slot came to be refused for holding a Green Torch while
        # both the game and the grid showed a regular one.
        cur = self._live_inventory().read_slot(slot)
        # Stale-snapshot guard: the caller states what it believed the slot held. If the
        # game moved items since that snapshot, writing would template the caller's stale
        # item over whatever is really there, destroying it. Refuse instead.
        if expect_type is not None:
            if cur is None:
                raise ServiceError(
                    f"slot {slot} could not be read to verify it still holds "
                    f"{names.label(expect_type)} — refusing to write")
            if cur.type != expect_type:
                raise ServiceError(
                    f"slot {slot} now holds {names.label(cur.type)}, not "
                    f"{names.label(expect_type)} — it changed in-game. "
                    "Refresh and try again.")
        # Only re-template when the item type actually changes (field tweaks on the
        # same item must not wipe the edits, and must stay scan-free/fast).
        changed = cur is None or item_type != cur.type
        # type 0 clears the slot — never template-scan for the "empty" type.
        block = self._template_block(item_type) if (changed and item_type) else None
        if changed:
            self._place_item(invs, slot, item_type, block)
        for inv in invs:
            if stack is not None:
                inv.set_stack(slot, stack)
            if damage is not None:
                inv.set_damage(slot, damage)
            if auto_reuse is not None:
                inv.set_auto_reuse(slot, bool(auto_reuse))
            if use_time is not None:
                inv.set_use_speed(slot, use_time, use_anim)
            if pick is not None:
                inv.set_pick(slot, pick)
            if tile_boost is not None:
                inv.set_tile_boost(slot, tile_boost)
            if defense is not None:
                inv.set_defense(slot, defense)
            if prefix is not None:
                inv.set_prefix(slot, prefix)

    def give_item(self, item_type: int, stack: int = 1) -> int:
        """Put a fully-statted item in the first empty main-inventory slot."""
        invs = self._all_inventories()
        # Which slot is free must be read from the LIVE copy: an inert snapshot shows
        # whatever was there when it was taken, so trusting it can pick a slot that is
        # empty in the snapshot and occupied in the game — and the give would then land
        # on top of a real item and destroy it. (Same class of bug as the stale guard in
        # set_item; that one refused a legitimate edit, this one loses an item.)
        by_slot = {s.index: s for s in self._live_inventory().slots()}
        empty = next((i for i in GIVE_RANGE
                      if i in by_slot and by_slot[i].empty), None)
        if empty is None:
            raise ServiceError("inventory full — no empty slot to give into")
        block = self._template_block(item_type)
        self._place_item(invs, empty, item_type, block)
        for inv in invs:
            inv.set_stack(empty, stack)
        return empty

    # --- NPC spawning ------------------------------------------------------
    def _npc_template_block(self, net_id: int) -> bytes | None:
        """The ContentSamples template object for one netID, whole.

        Rescanned rather than cached by address, for the same reason `_template_block`
        is: the managed heap is collected, so a remembered address stops being the object
        it was. The parsed-stats cache is a different thing and stays valid, because it
        holds numbers rather than addresses.
        """
        import numpy as np

        from terrariabonker import npcs as npc_mod
        vt = npc_mod.find_npc_vtable(self.mem)
        if vt is None:
            return None
        span = npc_mod.NPC_OBJECT_SIZE
        best = None
        for start, end in self.mem.regions():
            buf = self.mem.read(start, end - start)
            n = len(buf) // 4
            if n < 1:
                continue
            arr = np.frombuffer(buf[: n * 4], dtype=np.uint32)
            for idx in np.where(arr == vt)[0].tolist():
                off = idx * 4
                if off + span > len(buf):
                    continue
                nid = int.from_bytes(buf[off + npc_mod.NPC_NET_ID:
                                         off + npc_mod.NPC_NET_ID + 4], "little",
                                     signed=True)
                # A template is inactive; a live NPC of the same type is not, and its
                # stats have been scaled by the world's difficulty.
                if nid == net_id and buf[off + npc_mod.NPC_ACTIVE] == 0:
                    best = buf[off:off + span]
        return best

    def _free_npc_slot(self, arr: int) -> tuple[int, int] | None:
        """``(index, object address)`` of an unused Main.npc slot, or None if full."""
        from terrariabonker import npcs as npc_mod
        from terrariabonker.inventory import ARR_DATA_OFF

        for i in range(npc_mod.MAX_NPCS):
            obj = self.mem.read_u32(arr + ARR_DATA_OFF + i * 4)
            if obj and self.mem.read(obj + npc_mod.NPC_ACTIVE, 1) == b"\x00":
                return i, obj
        return None

    def spawn_npc(self, net_id: int, distance_tiles: int = 25) -> dict:
        """Spawn an NPC beside the player by copying its template into a free slot.

        This is `give_item`'s trick one level up: the game keeps a fully-populated
        template of every NPC, and every Main.npc slot is a real NPC object allocated at
        world load, so a spawn is a field copy plus a position — no code injection, no
        managed call, nothing to record in the build ledger.

        `active` is written last, on purpose: until it is set the game skips the slot
        entirely, so it never sees a half-built NPC.
        """
        import struct

        from terrariabonker import npcs as npc_mod
        from terrariabonker.locate import STATLIFE_FROM_OBJ

        player = self.live_block()
        arr = npc_mod.find_npc_array(self.mem)
        if arr is None:
            raise ServiceError("could not find Main.npc — is a world loaded?")
        block = self._npc_template_block(net_id)
        if block is None:
            raise ServiceError(f"no template for NPC {net_id} ({npc_mod.label(net_id)})")
        free = self._free_npc_slot(arr)
        if free is None:
            raise ServiceError("no free NPC slot — the world is at its NPC limit")
        slot, obj = free

        base = player.life_addr - STATLIFE_FROM_OBJ
        px, py = struct.unpack("<ff", self.mem.read(base + npc_mod.NPC_POSITION_X, 8))
        facing = self.mem.read_i32(base + 0x2C) or 1
        # Behind the player, so a spawn never lands on top of them. Clamped away from the
        # world edge, where a negative coordinate would put the NPC outside the map.
        x = max(100.0 * 16, px - facing * distance_tiles * 16.0)

        for lo, hi in npc_mod.NPC_COPY_SPANS:
            self.mem.write(obj + lo, block[lo:hi])
        self.mem.write(obj + npc_mod.NPC_WHO_AMI, struct.pack("<i", slot))
        self.mem.write(obj + npc_mod.NPC_POSITION_X, struct.pack("<ff", x, py))
        self.mem.write(obj + npc_mod.NPC_OLD_POSITION_X, struct.pack("<ff", x, py))
        self.mem.write(obj + npc_mod.NPC_VELOCITY_X, struct.pack("<ffff", 0, 0, 0, 0))
        self.mem.write(obj + npc_mod.NPC_ACTIVE, b"\x01")

        return {"slot": slot, "id": net_id, "name": npc_mod.label(net_id),
                "x": x / 16.0, "y": py / 16.0, "tiles_away": distance_tiles}

    def compendium(self, refresh: bool = False) -> dict:
        """The full catalog: every item with its stats and kind, plus every NPC name.

        Item stats come from the game's own template objects (see ``content``), which needs
        one scan of the writable regions — about two seconds — so the result is cached per
        build under ``~/.cache`` (not the config dir, which can be root-owned) and reused.
        NPC stats are not read yet; they need ``Main.npc`` to locate the NPC vtable, which
        arrives with the spawn work.
        """
        from terrariabonker import content, names, npcs
        stats = self._item_template_cache(refresh)
        items = []
        for tid, name in sorted(names.all_names().items()):
            st = stats.get(tid)
            items.append({
                "id": tid, "name": name, "kind": content.item_kind(st) if st else "Unknown",
                "tooltip": names.tooltip(tid), "stats": st or {},
                "wiki": content.wiki_url(name),
            })
        npc_stats = self._npc_template_cache(refresh)
        self._publish_npc_draw_data(npc_stats)
        npc_list = []
        for nid, nm in sorted(npcs.all_names().items()):
            st = npc_stats.get(nid)
            npc_list.append({
                "id": nid, "name": nm, "npc": True,
                "kind": content.npc_kind(st) if st else "NPC",
                "stats": st or {}, "wiki": content.wiki_url(nm),
            })
        return {"items": items, "npcs": npc_list, "build": self.build_key()}

    def _live_npc_addrs(self) -> set:
        """Addresses of the NPCs currently in the world. Their stats have been scaled by
        the world's difficulty, so they are not templates (spec 039)."""
        from terrariabonker import npcs as npc_mod
        from terrariabonker.inventory import ARR_DATA_OFF

        out = set()
        arr = npc_mod.find_npc_array(self.mem)
        if not arr:
            return out
        for i in range(npc_mod.MAX_NPCS):
            obj = self.mem.read_u32(arr + ARR_DATA_OFF + i * 4)
            if obj:
                out.add(obj)
        return out

    def _publish_npc_draw_data(self, npc_stats: dict) -> None:
        """Hand the sprite extractor what only this side can read.

        ``Main.npcFrameCount`` and ``NPC.color`` live in the game's memory; extraction runs
        without sudo. Written through the same give-back-to-the-user path as the caches,
        for the same reason.

        The tints are keyed by netID rather than type on purpose: every coloured slime
        shares one neutral sheet and differs only by this colour.
        """
        from terrariabonker import npcs as npc_mod
        from terrariabonker import proc, sprites

        counts = npc_mod.read_frame_counts(self.mem)
        if not counts:
            return
        tints = {nid: {"type": st["type"], "color": st["color"]}
                 for nid, st in npc_stats.items()
                 if any(st.get("color") or ())}
        sprites.save_npc_draw_data(counts, tints)
        proc.give_back_to_user(sprites._NPC_FRAMES_FILE)

    def _npc_template_cache(self, refresh: bool = False) -> dict[int, dict]:
        """``{net_id: stats}`` for every NPC, cached per build like the item templates."""
        from terrariabonker import content, npcs

        def scan():
            vt = npcs.find_npc_vtable(self.mem)
            if not vt:
                return {}
            return content.find_npc_templates(self.mem, vt, self._live_npc_addrs())

        return self._template_cache("npcs", scan, refresh)

    def _item_template_cache(self, refresh: bool = False) -> dict[int, dict]:
        from terrariabonker import content

        return self._template_cache(
            "templates",
            lambda: content.find_item_templates(self.mem, self._item_vtable(),
                                                self._own_item_addrs()), refresh,
            # A cache written before spec 038 lacks these, and without them an edit
            # cannot be told from an item's own defaults. Rescan rather than guess.
            wants=("use_anim", "auto_reuse", "tile_boost"))

    def _template_cache(self, kind: str, scan, refresh: bool = False,
                        wants: tuple = ()) -> dict[int, dict]:
        import json
        import os

        from terrariabonker import proc
        path = os.path.expanduser("~/.cache/terrariabonker/%s-%s.json"
                                  % (kind, self.build_key().replace("+", "-")))
        if not refresh:
            try:
                with open(path) as f:
                    got = {int(k): v for k, v in json.load(f).items()}
                sample = next(iter(got.values()), None)
                if not wants or sample is None or all(w in sample for w in wants):
                    return got
            except (OSError, ValueError):
                pass
        found = scan()
        try:
            cache_dir = os.path.dirname(path)
            os.makedirs(cache_dir, exist_ok=True)
            tmp = path + ".tmp"
            with open(tmp, "w") as f:
                json.dump({str(k): v for k, v in found.items()}, f)
            os.replace(tmp, path)
            # This runs privileged but writes into the user's own cache directory (sudo -E
            # keeps HOME), so hand the result back or they cannot clear their own cache.
            proc.give_back_to_user(cache_dir)
            proc.give_back_to_user(path)
        except OSError:
            pass                      # a cache we cannot write is not a failure
        return found

    def build_key(self) -> str:
        """Identity of the running build, e.g. "1.4.5.7+24893155" (see version.build_key)."""
        version, buildid, _level, _msg = self._build_info()
        return ver.build_key(version, buildid)

    def patcher(self):
        """A Patcher bound to this game process (the code-patch cheats)."""
        from terrariabonker.patcher import Patcher
        return Patcher(self.mem)

    def tilemap(self):
        """A read-only view of the world's tiles (spec 040).

        ``main_static_base`` is a full memory scan and costs ~1.5s; building the TileMap
        from it is six reads. The statics do not move while the process lives, so the base
        is cached and only the cheap part is redone -- which matters because the vein
        watcher calls this on every trigger, and paying 1.5s there made the extractor look
        like it had a delay when the mining itself takes 20ms. A world change moves
        ``Main.tile``, not the statics, so the TileMap is still rebuilt each call.
        """
        from terrariabonker.locate import main_static_base
        from terrariabonker.tiles import TileMap

        if self._main_base is None:
            self._main_base = main_static_base(self.mem)
        base = self._main_base
        if base is None:
            raise ServiceError("could not locate Main's statics — is the game in a world?")
        try:
            return TileMap(self.mem, base)
        except ValueError as e:
            self._main_base = None            # stale or no world: rescan next time
            raise ServiceError(str(e))

    def vein_at(self, x: int, y: int, *, gems: bool = False,
                limit: int | None = None, diagonal: bool = True) -> dict:
        """What a vein miner *would* take, starting at one tile. Reads only.

        Deliberately a dry run: nothing about mining is destructive-free, so the part that
        decides which tiles to take is worth being able to inspect on its own.
        """
        from terrariabonker import tiles as T

        tm = self.tilemap()
        whitelist = T.whitelist(gems)
        t = tm.type_at(x, y)
        found = T.flood(tm, x, y, whitelist,
                        limit=limit or T.DEFAULT_LIMIT, diagonal=diagonal)
        return {
            "at": [x, y], "type": t,
            "name": (T.ORES.get(t) or T.EXTRACTABLES.get(t)
                     or T.WORLD_FORMED.get(t) or T.GEMS.get(t) or ""),
            "whitelisted": t in whitelist if t is not None else False,
            "tiles": [list(p) for p in found], "count": len(found),
            "capped": len(found) >= (limit or T.DEFAULT_LIMIT),
            "world": [tm.max_x, tm.max_y],
        }

    def player_tile(self) -> tuple[int, int]:
        """The tile the player is standing in."""
        import struct

        from terrariabonker.locate import STATLIFE_FROM_OBJ
        base = self.live_block().life_addr - STATLIFE_FROM_OBJ
        px, py = struct.unpack("<ff", self.mem.read(base + 0x0C, 8))
        return int(px // 16), int(py // 16)

    # Tiles that fall cannot be identified from a table we trust: the game's own
    # `Main.tileSand` holds only {53 Sand, 112 Ebonsand, 116 Pearlsand, 234 Crimsand}, yet
    # silt and slush demonstrably fall too, by some other mechanism. So the search for a
    # deposit that has moved does not classify tiles at all -- it looks down the whole
    # column and spends a fixed budget of reads doing it, which is correct for any falling
    # tile including ones we have not identified.
    FALL_SEARCH_READS = 40000

    def extract_vein(self, x: int, y: int, *, gems: bool = False,
                     limit: int | None = None, timeout: float = 20.0) -> dict:
        """Mine the vein at ``(x, y)`` through the game's own ``PickTile`` (spec 040).

        Tiles are handed over a batch at a time -- the stub mines a whole queue per frame,
        so a typical 10-30 tile vein goes at once. The batch is capped
        (``ORE_MAX_BATCH``) because draining a 400-tile vein in a single frame would run
        400 PickTiles in one frame, each spawning dust, drops and light updates.

        **The vein is re-found between batches rather than remembered.** Silt, slush and
        sand fall when the tile under them goes, so for a deposit bigger than one batch
        the coordinates from the first flood are stale by the second: the blocks are still
        there, just lower down. Re-flooding from whatever tile of the same id is still
        standing near where the vein was follows them down. Ores do not move, so for them
        this is the same list twice and costs one extra flood (~2ms).

        The stub only runs while the game is updating, so this waits for tiles to actually
        be **gone** rather than for the stub to acknowledge anything -- it only reads the
        queue. Waiting on the tiles is the better test regardless: it is the success
        condition rather than a proxy for it.
        """
        import time

        from terrariabonker import tiles as T
        from terrariabonker.patcher import ORE_MAX_BATCH

        p = self.patcher()
        if not p.is_enabled("ore_extract"):
            raise ServiceError("the ore extractor cheat is not enabled")
        tm = self.tilemap()
        want = T.whitelist(gems)
        cap = limit or T.DEFAULT_LIMIT
        first = T.flood(tm, x, y, want, cap)
        if not first:
            return {"at": [x, y], "queued": 0, "mined": 0, "left": 0, "batches": 0,
                    "waits": [], "median_wait": None, "reason": "not a whitelisted tile"}
        tid = tm.solid_type_at(x, y)

        # Where the rest of it can be later. Gravity is vertical, so a falling tile stays
        # in its OWN column and can only move down -- searching a box instead finds
        # unrelated deposits of the same ore below the vein and mines those too, which is
        # ore the player never asked for. Per column: from the vein's topmost tile there,
        # downwards, on a shared read budget.
        top: dict[int, int] = {}
        for qx, qy in first:
            top[qx] = min(top.get(qx, qy), qy)
        rows = max(8, self.FALL_SEARCH_READS // max(1, len(top)))

        def still_standing():
            """A live tile of this vein, wherever in its own columns it has settled.

            Walks down each column the vein occupied and **stops at the first solid tile
            that is not ours**: a falling pile rests on top of the ground it lands on, so
            anything under that ground is a different deposit. Without this the search
            runs to the world floor and happily mines an unrelated patch of the same ore
            a long way below -- which is what "it mines non-contiguous sections across
            the screen" turned out to be.
            """
            for xx, y_from in top.items():
                for yy in range(max(0, y_from), min(tm.max_y, y_from + rows)):
                    t = tm.solid_type_at(xx, yy)
                    if t == tid:
                        return (xx, yy)
                    if t is not None:
                        break                 # ground: the pile cannot be below this
            return None

        mined, batches, stalled = 0, 0, ""
        waits = []
        # A vein cannot grow. Whatever the re-scan turns up, never take more tiles than
        # the vein we were asked for held -- that is the backstop against following one
        # deposit into another.
        budget = min(cap, len(first))
        try:
            while mined < budget:
                seed = still_standing()
                if seed is None:
                    break                       # the whole deposit is gone
                batch = T.flood(tm, seed[0], seed[1], {tid},
                                budget - mined)[:ORE_MAX_BATCH]
                if not batch:
                    break
                if not p.ore_arm(batch):
                    stalled = "could not arm the stub"
                    break
                batches += 1
                t0 = time.time()
                deadline = t0 + timeout
                while time.time() < deadline:
                    if all(tm.solid_type_at(*q) is None for q in batch):
                        break
                    time.sleep(0.02)
                done = sum(1 for q in batch if tm.solid_type_at(*q) is None)
                waits.append(round(time.time() - t0, 3))
                mined += done
                if done == 0:
                    stalled = ("stopped early — nothing in a batch of %d broke within "
                               "%.0fs" % (len(batch), timeout))
                    break
        finally:
            p.ore_disarm()          # a queue left armed is re-mined every frame
        left = still_standing() is not None
        return {"at": [x, y], "queued": len(first), "mined": mined,
                "left": len(first) - mined if not left else -1,
                "batches": batches, "waits": waits,
                "median_wait": round(sorted(waits)[len(waits) // 2], 3) if waits else None,
                "reason": stalled}

    def vein_watch(self, *, gems: bool = False, limit: int | None = None,
                   radius: int | None = None, timeout: float = 8.0):
        """A stateful vein watcher. Call :meth:`_VeinWatch.round` repeatedly.

        Kept separate from any loop because the GUI cannot block: it drives this from a
        timer a few rounds at a time, while the CLI spins it in a loop. Both share the
        detection state, which has to persist between rounds -- rebuilding the ore map
        every call is the 0.29s cost that made the extractor miss the first tile.
        """
        return _VeinWatch(self, gems=gems, limit=limit, radius=radius, timeout=timeout)

    def watch_veins(self, *, gems: bool = False, limit: int | None = None,
                    radius: int | None = None, timeout: float = 8.0,
                    rounds: int | None = None, on_event=None) -> dict:
        """Mine the rest of a vein whenever the player breaks one of its tiles.

        This is the shape people expect from a vein miner (and what the tModLoader mod
        does): you break one ore by hand and the connected run goes with it, rather than
        naming a coordinate up front. Blocking; the GUI uses :meth:`watch_tick` instead.
        """
        import time

        w = self.vein_watch(gems=gems, limit=limit, radius=radius, timeout=timeout)
        taken, events, n = 0, [], 0
        try:
            while rounds is None or n < rounds:
                n += 1
                for e in w.round():
                    taken += e["mined"]
                    events.append(e)
                    if on_event:
                        on_event(e)
                time.sleep(0.01)
        finally:
            w.close()
        return {"rounds": n, "mined": taken, "events": events}

    def watch_tick(self, *, gems: bool = False, limit: int | None = None,
                   timeout: float = 8.0, budget: float = 0.08) -> dict:
        """Run the vein watcher for up to ``budget`` seconds and return what it took.

        The GUI drives this from a timer. It runs several rounds per call rather than one
        because a round costs ~0.02s while a round trip to the privileged worker costs
        rather more -- so batching them keeps detection tight without a timer firing at
        50Hz across a process boundary.
        """
        import time

        if self._watch is None:
            self._watch = self.vein_watch(gems=gems, limit=limit, timeout=timeout)
        end = time.time() + budget
        events, n = [], 0
        while time.time() < end:
            n += 1
            events.extend(self._watch.round())
            if events:
                break                       # hand results back promptly
            time.sleep(0.005)
        return {"rounds": n, "events": events,
                "mined": sum(e["mined"] for e in events)}

    def watch_stop(self) -> dict:
        """Drop the watcher and disarm. Called when the cheat is switched off."""
        w, self._watch = self._watch, None
        if w is not None:
            w.close()
        return {"stopped": w is not None}

    # --- fishing (spec 042) -----------------------------------------------

    #: What the kit hands out. The Golden Fishing Rod is the best in the game and Master
    #: Bait the strongest common bait — and bait power does double duty, because a higher
    #: power also lowers how often a bait is consumed.
    KIT_ROD = 2294
    KIT_BAIT = 2676
    KIT_BAIT_STACK = 30

    def fishing_kit(self) -> dict:
        """Give a rod and bait to a player who has neither. Idempotent.

        Deliberately gives nothing to a player who already has gear: a cheat that hands
        out another rod every time it is switched on would fill the inventory, and the
        rod someone chose is likelier to be the one they want.
        """
        inv = self._live_inventory()
        gear = inv.fishing_gear()
        gave = {}
        if not gear["rods"]:
            gave["rod"] = {"type": self.KIT_ROD, "slot": self.give_item(self.KIT_ROD)}
        if not gear["baits"]:
            gave["bait"] = {"type": self.KIT_BAIT,
                            "slot": self.give_item(self.KIT_BAIT, self.KIT_BAIT_STACK),
                            "stack": self.KIT_BAIT_STACK}
        after = self._live_inventory().fishing_gear()
        return {"gave": gave,
                "rods": [{"slot": s, "power": p} for s, p in after["rods"]],
                "baits": [{"slot": s, "power": p, "stack": n}
                          for s, p, n in after["baits"]]}

    def bait_tick(self, *, keep: int = 30) -> dict:
        """Top any bait stack below ``keep`` back up to it. One round.

        Stateless, like the potion round and for the same reason: the CLI runs it in a
        fresh process each time, so anything remembered between rounds would exist for the
        GUI and not for the CLI. "Keep at least N" needs no memory and says exactly what
        it does.
        """
        if keep < 1:
            raise ServiceError("keep must be at least 1")
        inv = self._live_inventory()
        topped = []
        for slot, power, stack in inv.fishing_gear()["baits"]:
            if stack < keep:
                for i in self._all_inventories():
                    i.set_stack(slot, keep)
                topped.append({"slot": slot, "power": power,
                               "was": stack, "now": keep})
        return {"keep": keep, "topped": topped,
                "baits": len(inv.fishing_gear()["baits"])}

    def watch_bait(self, *, keep: int = 30, interval: float = 1.0,
                   rounds: int | None = None, on_event=None) -> dict:
        """Keep bait topped up. Blocking; the GUI drives :meth:`bait_tick` from a timer."""
        import time

        n, refills = 0, 0
        while rounds is None or n < rounds:
            n += 1
            r = self.bait_tick(keep=keep)
            if r["topped"]:
                refills += len(r["topped"])
                if on_event:
                    on_event(r)
            time.sleep(interval)
        return {"rounds": n, "refills": refills}

    # --- passive potions (spec 041) ---------------------------------------

    def potion_tick(self, *, min_stack: int = 1, ticks: int | None = None) -> dict:
        """Renew the buff of every favorited potion in the inventory. One round.

        Stateless on purpose, unlike the vein watcher: there is nothing to remember
        between rounds, and re-reading the inventory each time is what makes favoriting a
        potion take effect immediately rather than at the next rescan.
        """
        from terrariabonker import buffs as B

        want = B.DEFAULT_TICKS if ticks is None else ticks
        inv = self._live_inventory()
        bar = B.Buffs(self.mem, self.live_block().life_addr)
        out: dict[str, list] = {"added": [], "renewed": [], "kept": [], "full": []}
        for slot, buff in inv.favorited_potions(min_stack):
            out[bar.renew(buff, want)].append({"slot": slot, "buff": buff})
        return {"ticks": want, "min_stack": min_stack,
                "carried": sum(len(v) for v in out.values()), **out}

    def watch_potions(self, *, min_stack: int = 1, ticks: int | None = None,
                      interval: float = 0.25, rounds: int | None = None,
                      on_event=None) -> dict:
        """Keep the buffs of favorited potions renewed. Blocking; the GUI uses a timer.

        ``interval`` must stay well under the buff time or the buff lapses between rounds
        and the player sees it flicker.
        """
        import time

        from terrariabonker import buffs as B

        want = B.DEFAULT_TICKS if ticks is None else ticks
        if interval * 60 >= want:
            raise ServiceError(
                f"a {interval}s interval cannot hold a {want}-tick buff up — "
                f"the buff would lapse between rounds")
        n, seen = 0, 0
        while rounds is None or n < rounds:
            n += 1
            r = self.potion_tick(min_stack=min_stack, ticks=want)
            if r["added"] or r["renewed"]:
                seen += len(r["added"]) + len(r["renewed"])
                if on_event:
                    on_event(r)
            time.sleep(interval)
        return {"rounds": n, "applied": seen}

    def frames_advancing(self, window: float = 0.08) -> bool:
        """Is the game actually simulating, or merely open?

        Terraria pauses in single-player when its window loses focus, and a paused game
        runs no frames. Sampled from Main's statics rather than the player, because a
        player standing still changes nothing — which cost a wrong diagnosis once.
        """
        import time

        from terrariabonker.locate import main_static_base

        try:
            base = self._main_base or main_static_base(self.mem)
            if base is None:
                return False
            a = self.mem.read(base, 0x400)
            time.sleep(window)
            return a != self.mem.read(base, 0x400)
        except Exception:
            return False

    def ensure_arena(self) -> bool:
        """Allocate our memory now, while the game happens to be running.

        Every cheat's stub lives in the arena, and allocating it means asking the game to
        call VirtualAlloc — which needs frames. But enabling a cheat from the panel means
        clicking the panel, which unfocuses the game, which pauses it. So the allocation
        must not wait until the user asks for a cheat: it is done opportunistically while
        the game is live, and by the time they toggle anything it is already there.

        Never raises and never blocks on a paused game: if frames are not advancing there
        is nothing to do yet, and the next poll will try again.
        """
        p = self.patcher()
        if p._arena and p._arena_ok(p._arena):
            return True
        if not self.frames_advancing():
            return False
        try:
            p.arena(timeout=3.0)
            return True
        except Exception:
            return False

    def build_check(self) -> dict:
        """Is this build one we know, and do the cheats still resolve on it? (spec 036)

        Answers the question the panel asks after a game update: what is running, has this
        machine already decided about it, and which cheats would work. Patches nothing.
        """
        from terrariabonker import builds
        from terrariabonker.patcher import ANCHORS

        key = self.build_key()
        version, buildid, level, msg = self._build_info()
        verified = any(key in a.verified for a in ANCHORS.values())
        decided = builds.decision(key)
        probe = self.patcher().probe(key)
        failed = sorted(n for n, r in probe.items() if not r["resolved"])
        return {
            "build": key, "version": version, "buildid": buildid,
            "runtime": ver.detect_runtime(self.mem),
            "level": level, "message": msg,
            "known": bool(verified), "verified_everywhere": all(
                key in a.verified for a in ANCHORS.values()),
            "decision": (decided or {}).get("decision"),
            "recognised": bool(verified) or decided is not None,
            "cheats": probe, "failed": failed,
        }

    def accept_build(self, how: str, failed=()) -> dict:
        """Record this machine's decision about the running build."""
        from terrariabonker import builds

        key = self.build_key()
        builds.remember(key, how, failed, runtime=ver.detect_runtime(self.mem))
        return {"build": key, "decision": how, "failed": sorted(failed)}

    def record_item_edit(self, item_type: int, kwargs: dict) -> dict:
        """Save the fields auto-restore needs: the ones the game regenerates from the type.

        Type, stack and prefix are written into the save by Terraria itself, so recording
        them achieves nothing and produced restore warnings about items whose only change
        was a prefix (spec 038).

        It deliberately does **not** try to drop fields that match the item's defaults.
        That was tried and it destroyed real edits: the "default" came from the
        ContentSamples scan, which can pick up a live edited item as the template, so an
        edit was compared against itself and pruned. Keeping a redundant field costs a
        harmless rewrite of a value the game would have set anyway; dropping a real one
        loses the user's work silently.
        """
        from terrariabonker import profile

        want = {k: v for k, v in (kwargs or {}).items()
                if k in profile.RESTORABLE and v is not None}
        profile.set_item_edit(item_type, want)
        return {"type": int(item_type), "saved": want}

    def restore(self) -> dict:
        """Re-apply the cross-session profile (desired cheats + item edits) to this game.

        Cheats first: each is re-enabled with its saved value; a cheat whose method isn't
        JIT-compiled yet raises and is reported as pending (not fatal) so the caller can
        retry. Items: re-apply an edit only when the slot still holds the same item type
        (re-applying stats to the same item) — a differing type means the player changed it,
        so skip to avoid clobbering; empty markers are never auto-cleared. Returns a report
        ``{"cheats": [...], "items": [...], "pending": [...], "skipped": [...]}``."""
        from terrariabonker import profile
        from terrariabonker.patcher import PatchError
        report = {"cheats": [], "items": [], "pending": [], "skipped": [], "absent": []}
        p = self.patcher()
        for name, value in profile.cheats().items():
            try:
                p.enable(name, value)
                report["cheats"].append(name)
            except PatchError:
                report["pending"].append(name)            # method not JIT-ready yet; retry
            except (KeyError, ServiceError):
                report["skipped"].append(f"cheat:{name}")
        # Matched by what the item *is*, not the slot it sat in: an edited weapon the
        # player moved used to lose its edit silently (spec 038).
        inv = list(self.inventory())
        for itype, fields in profile.item_edits().items():
            where = [c.slot for c in inv if c.type == itype]
            if not where:
                report["absent"].append(itype)        # ordinary, not a failure
                continue
            for slot in where:                        # every copy, not just the first
                self.set_item(slot, itype, **fields)
                report["items"].append(slot)
        return report

    def fast_mining(self, use_time: int = 8, use_anim: int = 13, pick: int = 200) -> list[int]:
        hit = []
        for inv in self._all_inventories():
            hit = inv.make_fast_mining(use_time, use_anim, pick)
        return hit

    def long_reach(self, tiles: int = 20) -> list[int]:
        hit = []
        for inv in self._all_inventories():
            hit = inv.long_reach(tiles)
        return hit


__all__ = ["Service", "ServiceError", "Snapshot", "PlayerState", "ItemSlot",
           "INVENTORY_SLOTS", "GIVE_RANGE"]


class _VeinWatch:
    """Watches for the player breaking a whitelisted tile and takes the rest of its vein.

    Detection has to be quick or it misses the first tile: rescanning the window every
    round costs ~0.29s (32k tiles) and with fast-mining the player breaks several blocks
    inside that, so the vein only goes "after a few". Instead the window is scanned once
    to learn where the ore *is*, and each round re-checks only those tiles -- ~1200 of
    them, ~0.012s. The full scan is redone when the player moves away from where it was
    taken, on a slow heartbeat, and after a vein is mined.

    The window must cover how far the player can actually mine, not a number that looks
    reasonable: with the tool-reach cheat on they break tiles 75 away, and a smaller
    window silently stops triggering the moment they mine at range.
    """

    RESCAN_EVERY = 3.0        # heartbeat: catches ore revealed by someone else digging
    RESCAN_MOVE = 12          # tiles the player may drift before the window is stale
    STEPS = ((1, 0), (-1, 0), (0, 1), (0, -1), (1, 1), (1, -1), (-1, 1), (-1, -1))

    def __init__(self, svc, *, gems=False, limit=None, radius=None, timeout=8.0):
        from terrariabonker import tiles as T

        self.svc = svc
        self.gems, self.limit, self.timeout = gems, limit, timeout
        self.p = svc.patcher()
        if not self.p.is_enabled("ore_extract"):
            raise ServiceError("the ore extractor cheat is not enabled")
        self.want = T.whitelist(gems)
        if radius is None:
            reach = (self.p.values() or {}).get("tool_reach")
            radius = int(reach) + 15 if reach else 90
        self.radius = radius
        self.tm = svc.tilemap()
        self.tracked: dict[tuple[int, int], int] = {}
        self.at = None
        self.last_full = 0.0

    def _scan(self, px, py):
        tm, want, r = self.tm, self.want, self.radius
        return {(x, y): t
                for y in range(max(0, py - r), min(tm.max_y, py + r))
                for x in range(max(0, px - r), min(tm.max_x, px + r))
                if (t := tm.solid_type_at(x, y)) in want}

    def round(self) -> list[dict]:
        """One detection round. Returns a result dict per vein taken (usually none)."""
        import time

        from terrariabonker import tiles as T          # noqa: F401  (whitelist already read)

        tm = self.tm
        px, py = self.svc.player_tile()
        now = time.time()
        if (self.at is None or now - self.last_full > self.RESCAN_EVERY
                or max(abs(px - self.at[0]), abs(py - self.at[1])) > self.RESCAN_MOVE):
            self.tracked = self._scan(px, py)
            self.at, self.last_full = (px, py), now
        # the fast path: only tiles we already know are ore
        broke = [(xy, t) for xy, t in self.tracked.items()
                 if tm.solid_type_at(*xy) is None]
        for xy, _ in broke:
            self.tracked.pop(xy, None)
        out = []
        for (bx, by), tid in broke:
            # the tile is gone, so the vein is whatever neighbour of the same id is still
            # standing: breaking copper takes copper, not the iron behind it
            start = next((q for q in ((bx + dx, by + dy) for dx, dy in self.STEPS)
                          if tm.solid_type_at(*q) == tid), None)
            if start is None:
                continue                              # a lone tile: nothing connected
            got = self.svc.extract_vein(start[0], start[1], gems=self.gems,
                                        limit=self.limit, timeout=self.timeout)
            got["triggered_by"] = [bx, by]
            out.append(got)
        if broke:
            px, py = self.svc.player_tile()
            self.tracked = self._scan(px, py)
            self.at, self.last_full = (px, py), time.time()
        return out

    def close(self):
        try:
            self.p.ore_disarm()
        except Exception:
            pass
