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

    def invalidate(self) -> None:
        """Drop cached locate results; the next call rescans from scratch."""
        self._blocks = None
        self._anchor = None
        self._compat = None

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
        """Every player copy's inventory. Writes go to all so the live copy is
        always hit (copies are identical; snapshots ignore the writes) - which
        copy is 'live' can't be told apart while the game is paused."""
        return [Inventory(self.mem, b.life_addr) for b in self.players()]

    def _item_vtable(self) -> int | None:
        """The shared mono vtable of Item objects, read from any real item."""
        for s in self._all_inventories()[0].slots():
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
        cur = invs[0].read_slot(slot)
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
        by_slot = {s.index: s for s in invs[0].slots()}
        empty = next((i for i in GIVE_RANGE
                      if i in by_slot and by_slot[i].empty), None)
        if empty is None:
            raise ServiceError("inventory full — no empty slot to give into")
        block = self._template_block(item_type)
        self._place_item(invs, empty, item_type, block)
        for inv in invs:
            inv.set_stack(empty, stack)
        return empty

    def build_key(self) -> str:
        """Identity of the running build, e.g. "1.4.5.7+24893155" (see version.build_key)."""
        version, buildid, _level, _msg = self._build_info()
        return ver.build_key(version, buildid)

    def patcher(self):
        """A Patcher bound to this game process (the code-patch cheats)."""
        from terrariabonker.patcher import Patcher
        return Patcher(self.mem)

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
        report = {"cheats": [], "items": [], "pending": [], "skipped": []}
        p = self.patcher()
        for name, value in profile.cheats().items():
            try:
                p.enable(name, value)
                report["cheats"].append(name)
            except PatchError:
                report["pending"].append(name)            # method not JIT-ready yet; retry
            except (KeyError, ServiceError):
                report["skipped"].append(f"cheat:{name}")
        cur = {s.slot: s for s in self.inventory()}
        for slot_s, kw in profile.items().items():
            slot, itype = int(slot_s), int(kw.get("type", 0))
            if not itype:
                continue                                  # empty marker: never auto-clear
            c = cur.get(slot)
            if c is not None and c.type == itype:         # same item -> re-apply the edits
                self.set_item(slot, itype,
                              **{k: v for k, v in kw.items() if k != "type"})
                report["items"].append(slot)
            else:
                report["skipped"].append(f"item:slot{slot}")  # item changed; don't clobber
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
