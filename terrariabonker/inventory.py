"""Terraria inventory reading and editing.

The inventory is reached structurally, not by value-scanning a stack count:
scanning finds only downstream caches (a crafting aggregate, a UI mirror) whose
writes do not affect the game. The real source is ``Player.inventory``, an
``Item[]`` of 59 slots reached through a pointer at ``statLife - 0x664``.

mono szarray layout (32-bit): ``+0xC`` max_length, ``+0x10`` element pointers.
Every slot holds a real ``Item`` (empty slots are type 0, never null). Within an
``Item``: type at ``+0x6C`` (ItemID), stack at ``+0x88``, damage at ``+0xAC``, prefix at
``+0x15C`` (modifier tier, 0 when unmodified). The constants below are the authority --
this paragraph said the prefix was at ``+0xAC``, which is the damage field.

Offsets are for Terraria 1.4.5.7. See docs/discovery.md.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

from terrariabonker import layout

INVENTORY_PTR_OFF = -0x664      # Player field holding the Item[] pointer, from statLife
INVENTORY_SLOTS = 59
# Player.selectedItem -- the hotbar index, 0..9 -- from statLife. Found by watching which
# ints track the hotbar and checking each one against the slot the player was holding:
# it named Slime Whip, Boomstick and Book of Skulls correctly as they switched. A twin at
# statLife-0x690 never disagreed across 2473 samples; this is the lower of the pair.
SELECTED_ITEM_OFF = -0x694

# Re-exported from `layout`, which is the one place these are declared -- several modules
# import them from here, and this keeps those imports working.
ARR_LEN_OFF = layout.ARR_LEN_OFF
ARR_DATA_OFF = layout.ARR_DATA_OFF

# Item field offsets, derived by diffing Copper Pickaxe (3509) vs Copper Axe (3506).
ITEM_USE_ANIM = 0x80    # useAnimation (visual swing frames)
ITEM_USE_TIME = 0x84    # useTime (ticks per use; lower = faster mining/placing/attack)
ITEM_TYPE = 0x6C
ITEM_STACK = 0x88
ITEM_TILEBOOST = 0x9C   # extra tiles of placement reach (added to base tileRangeX/Y)
ITEM_PICK = 0x90        # pickaxe power %
ITEM_AXE = 0x94         # axe power (x5 = displayed %)
ITEM_HAMMER = 0x98      # hammer power %
ITEM_DAMAGE = 0xAC      # weapon/tool damage (-1 for non-damaging items)
# Fields a modifier scales (spec 046). All four are verified in docs/item-fields.md;
# knockBack was re-confirmed against templates whose values the wiki publishes (Copper
# Broadsword 5.5, Meowmere 6.5). `crit`, `armorPenetration` and `bonusTagDamage` are NOT
# here on purpose: declaration order puts them at 0x150/0x154/0x158 and every template
# reads 0 there, so nothing distinguishes them from padding. Writing an unverified offset
# into an item is permanent, so they are left unapplied until they are verified.
ITEM_KNOCKBACK = 0xB0   # float
ITEM_SCALE = 0xCC       # float; size, which is also melee reach
ITEM_SHOOT = 0x0FC              # Item.shoot -- the projectile type this item fires
ITEM_SHOOTSPEED = 0x100  # float
ITEM_MANA = 0x11C       # mana cost per use
# Verified 2026-08-27 by reforging a Minishark to Sighted at the Goblin Tinkerer and
# diffing the item: `crit` went 0 -> 3, which is exactly Sighted's +3, while damage went
# 12 -> 13 (x1.1). Definitive, and the method the spec named -- no candidate was written
# to find it. `armorPenetration` (0x154?) and `bonusTagDamage` (0x158?) are the next two
# ints and are predicted by the same declaration order that placed crit correctly, but
# Sighted grants neither, so neither was observed. They stay unwritten.
ITEM_CRIT = 0x150       # crit chance bonus; ADDED by a modifier, not multiplied
ITEM_DEFENSE = 0xD4     # defense the item grants (armor/some accessories; 0 otherwise)
ITEM_CONSUMABLE = 0xBD  # byte bool; if set, the item is used up on use (careful!)
ITEM_AUTOREUSE = 0xBE   # byte bool; auto-swing while the button is held
ITEM_RARE = 0xF8        # rarity tier (int): -1 gray .. 0 white .. 10 red .. 11 purple
ITEM_PREFIX = 0x15C     # modifier tier (byte): 0 none .. e.g. Legendary/Warding/Menacing
# Damage-class / accessory flags (byte bools) — used to offer only item-appropriate
# modifiers in the editor and to label a modified item.
# Derived by differencing the game's own template objects rather than by dissecting the
# class: the offset that is >= 0 for known helmets/chestplates/greaves and -1 for everything
# else, and likewise > 0 for healing/mana potions. Cross-checked (Skeletron Mask headSlot 98,
# Dirt Block -1) and the four sit adjacent, as sibling fields do.
ITEM_BUFF_TYPE = 0x130  # BuffID granted on use; buff potions carry no healLife/healMana
ITEM_HEAL_LIFE = 0xB4
ITEM_HEAL_MANA = 0xB8
ITEM_HEAD_SLOT = 0xD8
ITEM_BODY_SLOT = 0xDC
ITEM_LEG_SLOT = 0xE0

# Set by alt-click; the game uses it to protect a slot from quick-stack and from being
# sold. Derived live rather than by differencing templates, because a template is never
# favorited: three snapshots of the same six potions, favoriting and unfavoriting between
# them, and this is the byte that tracked the player's alt-clicks and nothing else. The
# rival reading was a "new item" glow flag, which looks identical at rest and is ruled out
# by the flag turning back ON when a favorited item is clicked.
ITEM_FAVORITED = 0x70   # byte bool

# Fishing (spec 042). Both derived by differencing templates and checked against the
# game's own numbers: exactly 7 items carry a nonzero fishingPole and every one is a rod
# (Golden 50 down to Chum Caster 25); 13 carry bait and none of them is a rod.
ITEM_FISHING_POLE = 0x58   # byte: the rod's fishing power
ITEM_BAIT = 0x5C           # byte: the bait's power

ITEM_ACCESSORY = 0x7D
ITEM_MELEE = 0x15D
ITEM_MAGIC = 0x15E
ITEM_RANGED = 0x15F
ITEM_SUMMON = 0x160


@dataclass
class Slot:
    index: int
    item_addr: int
    type: int
    stack: int
    use_time: int
    use_anim: int
    pick: int
    tile_boost: int
    damage: int
    auto_reuse: int
    rare: int
    defense: int
    prefix: int
    flags: dict          # damage-class / accessory bools: melee/ranged/magic/summon/accessory

    @property
    def empty(self) -> bool:
        return self.type == 0

    @property
    def is_pickaxe(self) -> bool:
        return self.pick > 0


class Inventory:
    """The inventory of one player copy, addressed by that copy's statLife."""

    def __init__(self, mem, life_addr: int):
        self.mem = mem
        self.life = life_addr

    def array_addr(self) -> int | None:
        """Resolve the current Item[] address (re-read so it self-corrects)."""
        ptr = self.mem.read_u32(self.life + INVENTORY_PTR_OFF)
        return ptr or None

    def _item_addr(self, index: int) -> int | None:
        arr = self.array_addr()
        if arr is None:
            return None
        return self.mem.read_u32(arr + ARR_DATA_OFF + index * 4)

    #: Where each modifier-scaled field lives, for reading an item's base stats out of a
    #: ContentSamples template block.
    PREFIX_BASE_FIELDS: dict[str, tuple] = {
        "damage": (ITEM_DAMAGE, "i32"),
        "knockback": (ITEM_KNOCKBACK, "f32"),
        "useanim": (ITEM_USE_ANIM, "i32"),
        "usetime": (ITEM_USE_TIME, "i32"),
        "scale": (ITEM_SCALE, "f32"),
        "shootspeed": (ITEM_SHOOTSPEED, "f32"),
        "mana": (ITEM_MANA, "i32"),
        "crit": (ITEM_CRIT, "i32"),
    }

    def read_slot(self, index: int) -> Slot | None:
        addr = self._item_addr(index)
        if not addr:
            return None
        return Slot(
            index=index,
            item_addr=addr,
            type=self.mem.read_i32(addr + ITEM_TYPE),
            stack=self.mem.read_i32(addr + ITEM_STACK),
            use_time=self.mem.read_i32(addr + ITEM_USE_TIME),
            use_anim=self.mem.read_i32(addr + ITEM_USE_ANIM),
            pick=self.mem.read_i32(addr + ITEM_PICK),
            tile_boost=self.mem.read_i32(addr + ITEM_TILEBOOST),
            damage=self.mem.read_i32(addr + ITEM_DAMAGE),
            auto_reuse=self.mem.read(addr + ITEM_AUTOREUSE, 1)[0] if addr else 0,
            rare=self.mem.read_i32(addr + ITEM_RARE),
            defense=self.mem.read_i32(addr + ITEM_DEFENSE),
            prefix=self.mem.read(addr + ITEM_PREFIX, 1)[0] if addr else 0,
            flags={
                "accessory": bool(self.mem.read(addr + ITEM_ACCESSORY, 1)[0]),
                "melee": bool(self.mem.read(addr + ITEM_MELEE, 1)[0]),
                "magic": bool(self.mem.read(addr + ITEM_MAGIC, 1)[0]),
                "ranged": bool(self.mem.read(addr + ITEM_RANGED, 1)[0]),
                "summon": bool(self.mem.read(addr + ITEM_SUMMON, 1)[0]),
            },
        )

    def slots(self) -> list[Slot]:
        out = []
        for i in range(INVENTORY_SLOTS):
            s = self.read_slot(i)
            if s is not None:
                out.append(s)
        return out

    def find_type(self, item_type: int) -> list[int]:
        """Indices of slots holding the given ItemID."""
        return [s.index for s in self.slots() if s.type == item_type]

    # The four fields passive potions (spec 041) care about span 0x70..0x134, so one read
    # per item covers all of them. Four separate reads would be four syscalls per slot and
    # this runs on a timer several times a second.
    _POTION_LO = ITEM_FAVORITED
    _POTION_HI = ITEM_BUFF_TYPE + 4

    def favorited_potions(self, min_stack: int = 1) -> list[tuple[int, int]]:
        """``(slot index, buff type)`` for each favorited consumable carrying a buff.

        The favorite is the player's opt-in: without it, every potion picked up would
        start doing something. ``consumable`` is what keeps pets, light pets and mounts
        out -- they carry a ``buffType`` too, and summoning a pet because it was in the
        bag is not what anyone means by a potion.
        """
        out: list[tuple[int, int]] = []
        arr = self.array_addr()
        if arr is None:
            return out
        lo, width = self._POTION_LO, self._POTION_HI - self._POTION_LO
        for i in range(INVENTORY_SLOTS):
            addr = self.mem.read_u32(arr + ARR_DATA_OFF + i * 4)
            if not addr:
                continue
            try:
                w = self.mem.read(addr + lo, width)
            except OSError:
                continue
            if not w[ITEM_FAVORITED - lo] or not w[ITEM_CONSUMABLE - lo]:
                continue                       # the two cheap byte gates first
            if struct.unpack_from("<i", w, ITEM_STACK - lo)[0] < min_stack:
                continue
            buff = struct.unpack_from("<i", w, ITEM_BUFF_TYPE - lo)[0]
            if buff > 0:
                out.append((i, buff))
        return out

    def set_fishing_power(self, index: int, value: int) -> bool:
        """Write a rod's fishing power. A byte field, so anything over 255 would wrap."""
        addr = self._item_addr(index)
        if not addr or not 0 <= value <= 255:
            return False
        self.mem.write(addr + ITEM_FISHING_POLE, bytes([value]))
        return True

    def fishing_gear(self, index: int | None = None) -> dict:
        """``{"rods": [(slot, power)], "baits": [(slot, power, stack)]}``.

        Both lists rather than a first hit: a player may carry several rods, and topping
        up one bait stack while another runs dry is not "bait never runs out".
        """
        out: dict[str, list] = {"rods": [], "baits": []}
        arr = self.array_addr()
        if arr is None:
            return out
        for i in range(INVENTORY_SLOTS):
            addr = self.mem.read_u32(arr + ARR_DATA_OFF + i * 4)
            if not addr:
                continue
            try:
                w = self.mem.read(addr + ITEM_FISHING_POLE, 8)
            except OSError:
                continue
            if not self.mem.read_i32(addr + ITEM_TYPE):
                continue
            pole, bait = w[0], w[ITEM_BAIT - ITEM_FISHING_POLE]
            if pole:
                out["rods"].append((i, pole))
            if bait:
                out["baits"].append((i, bait, self.mem.read_i32(addr + ITEM_STACK)))
        return out

    def selected_slot(self) -> int | None:
        """Which hotbar slot the player is holding, or None if it reads implausibly."""
        v = self.mem.read_i32(self.life + SELECTED_ITEM_OFF)
        return v if v is not None and 0 <= v <= 9 else None

    def holding_rod(self) -> bool:
        """Is a fishing rod in the player's hand right now?

        Auto-catch needs this before it presses anything on empty water: the use button
        is not fishing-specific, so "cast again" against a sword is "swing your sword",
        which is what it did when it could only see the water and not the hand.
        """
        slot = self.selected_slot()
        if slot is None:
            return False
        return slot in {s for s, _ in self.fishing_gear()["rods"]}

    def nonempty_count(self) -> int:
        """How many slots hold a real item.

        A distinguishing signal for the live player: its inventory reflects
        actual play, while the load-time snapshot copies hold only the starting
        items. Used to pick the live copy when activity sampling is inconclusive
        (e.g. the game is paused).
        """
        return sum(1 for s in self.slots() if not s.empty)

    # --- edits -------------------------------------------------------------
    def set_stack(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_STACK, value) if addr else False

    def set_type(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_TYPE, value) if addr else False

    def set_damage(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_DAMAGE, value) if addr else False

    def set_auto_reuse(self, index: int, on: bool = True) -> bool:
        """Toggle auto-swing (hold to attack) on the item in this slot."""
        addr = self._item_addr(index)
        return self.mem.write(addr + ITEM_AUTOREUSE, bytes([1 if on else 0])) if addr else False

    def set_use_speed(self, index: int, use_time: int, use_anim: int | None = None) -> bool:
        """Set swing speed (mining/placing/attacking). Lower = faster."""
        addr = self._item_addr(index)
        if not addr:
            return False
        ok = self.mem.write_i32(addr + ITEM_USE_TIME, use_time)
        ok = self.mem.write_i32(addr + ITEM_USE_ANIM,
                                use_time if use_anim is None else use_anim) and ok
        return ok

    def set_pick(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_PICK, value) if addr else False

    def set_defense(self, index: int, value: int) -> bool:
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_DEFENSE, value) if addr else False

    def set_prefix(self, index: int, value: int) -> bool:
        """Set the item's modifier tier (a byte, e.g. Legendary/Warding).

        The byte alone is only the name. Bonuses live in the item's own fields -- see
        :meth:`apply_prefix_stats`, which the service calls with the item's base stats.
        """
        addr = self._item_addr(index)
        return self.mem.write(addr + ITEM_PREFIX, bytes([value & 0xFF])) if addr else False

    #: Modifier stat -> the fields it scales, as ``(base key, offset, kind)``.
    #: One multiplier can drive several fields -- in the game `usetime` scales
    #: useAnimation, useTime and reuseDelay -- and each scales from ITS OWN base, which is
    #: why the base key is carried per field rather than per stat. useAnimation and useTime
    #: are equal on most weapons and not on all, so sharing one base would quietly rewrite
    #: one of them to the other's value. (reuseDelay is not here: its offset is unknown.)
    _PREFIX_FIELDS: dict[str, tuple] = {
        "damage": (("damage", ITEM_DAMAGE, "i32"),),
        "knockback": (("knockback", ITEM_KNOCKBACK, "f32"),),
        "usetime": (("useanim", ITEM_USE_ANIM, "i32"), ("usetime", ITEM_USE_TIME, "i32")),
        "scale": (("scale", ITEM_SCALE, "f32"),),
        "shootspeed": (("shootspeed", ITEM_SHOOTSPEED, "f32"),),
        "mana": (("mana", ITEM_MANA, "i32"),),
        "crit": (("crit", ITEM_CRIT, "i32"),),
    }

    #: Bonuses the game ADDS to the field rather than multiplying into it.
    _PREFIX_ADDITIVE = frozenset({"crit"})

    def apply_prefix_stats(self, index: int, mults: dict, base: dict) -> dict:
        """Scale the item's fields by a modifier's multipliers, from ``base``.

        ``base`` is the field values of a *pristine* item of this type, so applying the
        same modifier twice gives the same answer and switching modifiers cannot compound.
        Scaling the item's current values instead would drift a little further every time.

        Integer fields are rounded the way the game rounds them: .NET's `Math.Round` and
        Python's `round` are both round-half-to-even, so a `.5` lands on the same number in
        both.

        **Every scaled field is written, not only the ones this modifier changes.** A
        modifier the game applies always lands on a freshly reset item, so switching from
        Godly to Large has to put damage back to base rather than leave Godly's behind, and
        clearing the modifier has to restore everything. Fields this modifier does not
        touch simply get a multiplier of 1.

        Returns what it wrote, for the caller to report. Stats with no offset here (crit
        and the two other additive ones) are skipped and named in the result, rather than
        silently dropped -- a modifier that quietly loses its crit bonus is this bug again.
        """
        addr = self._item_addr(index)
        if addr is None:
            return {"written": {}, "skipped": sorted(mults)}
        written, skipped = {}, []
        for stat in mults:
            if stat not in self._PREFIX_FIELDS:
                skipped.append(stat)          # a bonus we have no verified offset for
        for stat, fields in self._PREFIX_FIELDS.items():
            additive = stat in self._PREFIX_ADDITIVE
            amount = mults.get(stat, 0.0 if additive else 1.0)
            for key, off, kind in fields:
                if key not in base:
                    skipped.append(key)
                    continue
                value = base[key] + amount if additive else base[key] * amount
                if kind == "i32":
                    self.mem.write_i32(addr + off, int(round(value)))
                else:
                    self.mem.write_f32(addr + off, value)
                if amount != (0.0 if additive else 1.0):
                    written[key] = value
        return {"written": written, "skipped": sorted(set(skipped))}

    def set_tile_boost(self, index: int, value: int) -> bool:
        """Set extra placement reach (tiles) for the item in this slot."""
        addr = self._item_addr(index)
        return self.mem.write_i32(addr + ITEM_TILEBOOST, value) if addr else False

    def long_reach(self, tiles: int = 20) -> list[int]:
        """Give every non-empty item extended placement reach. Returns slots hit.

        Harmless on non-placeable items (tileBoost only affects tile placement),
        so this is applied inventory-wide rather than only to the held slot.
        """
        hit = []
        for s in self.slots():
            if not s.empty:
                self.set_tile_boost(s.index, tiles)
                hit.append(s.index)
        return hit

    def make_fast_mining(self, use_time: int = 8, use_anim: int = 13,
                         pick: int | None = 200) -> list[int]:
        """Speed up every pickaxe in the inventory (persistent). Returns slots hit.

        Defaults are Picksaw-tier: fast but smooth against the 60 fps swing cap.
        A pickaxe is any item whose ``pick`` power is above zero.
        """
        hit = []
        for s in self.slots():
            if s.is_pickaxe:
                self.set_use_speed(s.index, use_time, use_anim)
                if pick is not None:
                    self.set_pick(s.index, pick)
                hit.append(s.index)
        return hit
