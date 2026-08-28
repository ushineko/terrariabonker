"""Per-type projectile overrides, enforced on live projectiles (spec 047).

Nothing here persists in the game. `Projectile.SetDefaults` assigns literals from its own
`if (type == N)` chain and never consults `ContentSamples.ProjectilesByType`, so there is
no write that makes a change stick -- every field has to be re-applied to each live
projectile. That is the whole reason this is a sweep rather than a one-shot edit.

Offsets are imported from :mod:`terrariabonker.projectiles`, which owns the Projectile
layout, and are checked against the mono runtime by ``tools/monofields.py --verify``.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

from terrariabonker import projectiles as P

ARRAY_LEN = P.ARRAY_LEN


@dataclass(frozen=True)
class Field:
    """One editable field: where it lives, how wide it is, and what it may be set to.

    ``once`` marks a field applied when a projectile is first seen rather than on every
    sweep -- see :class:`ProjectileEditor` for why ``timeLeft`` must be one of those.
    """

    offset: int
    kind: str                 # "i32" | "f32" | "b8"
    lo: float
    hi: float
    label: str
    once: bool = False

    def clamp(self, value):
        """Bound ``value`` to this field's range, in the field's own type."""
        v = max(self.lo, min(self.hi, float(value)))
        return v if self.kind == "f32" else int(round(v))


#: The v1 field set. Deliberately small: the metadata walker makes it trivial to offer all
#: 118 Projectile fields, and most of them are a crash or a self-inflicted death.
#: `aiStyle` runs an AI against `ai[]` slots that mean something else; `hostile`/`friendly`
#: turn the player's own projectiles on the player. Neither is reachable from here.
FIELDS: dict[str, Field] = {
    "tileCollide": Field(P.TILECOLLIDE_OFF, "b8", 0, 1, "Pass through blocks (0 = yes)"),
    "penetrate": Field(P.PENETRATE_OFF, "i32", -1, 999, "Enemies pierced (-1 = infinite)"),
    "extraUpdates": Field(P.EXTRAUPDATES_OFF, "i32", 0, 16, "Extra ticks per frame (speed)"),
    "scale": Field(P.SCALE_OFF, "f32", 0.05, 10.0, "Size"),
    "timeLeft": Field(P.TIMELEFT_OFF, "i32", 1, 216000, "Lifetime in ticks", once=True),
}


def _write(mem, addr: int, field: Field, value) -> None:
    """Write one field at its own width.

    Width is not a detail here. ``tileCollide`` is a single byte and ``extraUpdates``
    begins at the next word; a four-byte write of a bool reaches into whatever is packed
    behind it. The probe that preceded this module did exactly that.
    """
    if field.kind == "f32":
        mem.write(addr + field.offset, struct.pack("<f", float(value)))
    elif field.kind == "b8":
        mem.write(addr + field.offset, bytes([1 if value else 0]))
    else:
        mem.write_i32(addr + field.offset, int(value))


class ProjectileEditor:
    """Applies per-type overrides to whatever is currently in flight.

    Stateful across sweeps for one reason: some fields must be applied **once per
    projectile** rather than enforced.

    ``timeLeft`` is the case that forces it. Pinning it every sweep means no projectile
    ever expires, and the game allocates all 1001 slots up front -- so a pinned lifetime
    fills the array and the player's weapons quietly stop firing. Applied once, a raised
    ``timeLeft`` is a bigger budget the game then spends normally, which is what a player
    asking for "skulls that cross a wall" actually wants: `AI_001` drains 33 ticks per tick
    the skull is inside solid tile, so the budget is what decides how far it gets.

    A projectile is "new" when its slot holds a different object or a different type than
    the last sweep saw. Slots are recycled constantly by fast weapons, so identity has to
    come from the object, not the index.
    """

    def __init__(self):
        self._seen: dict[int, tuple[int, int]] = {}      # slot -> (object addr, type)

    def forget(self) -> None:
        """Drop per-projectile state, so every projectile counts as new again."""
        self._seen.clear()

    def sweep(self, mem, arr: int, overrides: dict[int, dict]) -> dict:
        """Apply ``overrides`` (projectile type -> {field: value}) once over the array.

        Returns counts rather than a bare total: "we wrote 40 fields" says nothing about
        whether the right projectiles were touched, and the caller reports to a player.
        """
        if not overrides:
            self._seen.clear()
            return {"patched": 0, "types": {}}

        raw = mem.read(arr + P.ARRAY_DATA_OFF, ARRAY_LEN * 4)
        if len(raw) < ARRAY_LEN * 4:
            return {"patched": 0, "types": {}}

        patched, per_type, live = 0, {}, {}
        for slot, obj in enumerate(struct.unpack(f"<{ARRAY_LEN}I", raw)):
            if not obj:
                continue
            if mem.read(obj + P.ACTIVE_OFF, 1) != b"\x01":
                continue
            ptype = mem.read_i32(obj + P.TYPE_OFF)
            wanted = overrides.get(ptype)
            if wanted is None:
                continue
            live[slot] = (obj, ptype)
            fresh = self._seen.get(slot) != (obj, ptype)
            for name, value in wanted.items():
                field = FIELDS.get(name)
                if field is None or (field.once and not fresh):
                    continue
                _write(mem, obj, field, field.clamp(value))
                # SetDefaults ends with `maxPenetrate = penetrate`; the game treats them as
                # a pair, so writing one alone leaves it accounting inconsistently.
                if name == "penetrate":
                    mem.write_i32(obj + P.MAXPENETRATE_OFF, int(field.clamp(value)))
                patched += 1
            per_type[ptype] = per_type.get(ptype, 0) + 1

        # Only slots seen this sweep are remembered: a slot the game has reused must not
        # match a projectile that left it, or `once` fields would never re-apply.
        self._seen = live
        return {"patched": patched, "types": per_type}
