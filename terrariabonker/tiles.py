"""Reading the world's tiles, and finding the contiguous run of ore around one (spec 040).

The layout is not guessed at: it is exactly what ``Player.PickTile`` does to reach a tile,
read out of its own JIT'd code.

    obj   = [Main.tile + 0x08]      # width @0, originX @4, height @8, originY @0xC
    idx   = height * (x - originX) + (y - originY)      # column-major
    entry = *(u32*)(Main.tile + 0x10 + 4*idx)          # one pointer per tile
    type  = *(u16*)(entry + 0x08)

**Two sizes, and they are not the same.** The bounds object above describes the *buffer*,
which the game allocates at the largest supported world size and reuses — it read 8401x2401
while a 4200x1200 world was loaded. Its height is the indexing stride. The world's real
extent is ``Main.maxTilesX``/``maxTilesY``, and that is what bounds a search: past it lie
tiles left over from whatever world was loaded before.
"""

from __future__ import annotations

import struct

MAIN_TILE_OFF = 0x99C           # Main.tile within Main's static block
MAIN_MAX_TILES_OFF = 0x5A4      # maxTilesX, then maxTilesY

_BOUNDS_OFF = 0x08              # -> {width, originX, height, originY}
_ENTRIES_OFF = 0x10             # the per-tile pointer array
_TILE_TYPE_OFF = 0x08           # ushort, within a tile object
_TILE_HEADER_OFF = 0x0C         # sTileHeader, ushort: type(2) wall(1) liquid(1)
_ACTIVE_BIT = 0x20              # Tile.active() is (sTileHeader & 32) == 32

# Vanilla ore tile ids. A whitelist is what keeps a flood fill from eating a region, so it
# is deliberately a list of ores rather than "anything that looks minable". Gems are listed
# separately because they are not ores and some players will not want them swept up.
#
# Every id here is checked against the game's own `TileID` constants by
# `tests/test_tiles.py`, which reads them out of `data/tiles.json`. The list started as a
# hand-written one and the check found two omissions — Luminite and Fossil Ore — which is
# exactly why it is checked rather than trusted.
ORES: dict[int, str] = {
    7: "Copper", 6: "Iron", 9: "Silver", 8: "Gold",
    166: "Tin", 167: "Lead", 168: "Tungsten", 169: "Platinum",
    22: "Demonite", 204: "Crimtane", 37: "Meteorite", 58: "Hellstone",
    107: "Cobalt", 221: "Palladium", 108: "Mythril", 222: "Orichalcum",
    111: "Adamantite", 223: "Titanium", 211: "Chlorophyte",
    408: "LunarOre", 407: "FossilOre",
}
# Not ores, but what an "ore extractor" is for: extractinator feedstock. Swept up by
# default, since leaving silt behind while mining a vein through it is not what anyone
# means by the feature.
EXTRACTABLES: dict[int, str] = {123: "Silt", 224: "Slush", 404: "DesertFossil"}
# Gems are neither, and are opt-in: some players are deliberately leaving them in place.
GEMS: dict[int, str] = {63: "Sapphire", 64: "Ruby", 65: "Emerald", 66: "Topaz",
                        67: "Amethyst", 68: "Diamond"}


def whitelist(gems: bool = False) -> set[int]:
    """The tile ids a vein miner may take."""
    out = set(ORES) | set(EXTRACTABLES)
    return out | set(GEMS) if gems else out


# A vein of contiguous ore is small; a cap keeps a mistake from walking the whole world.
DEFAULT_LIMIT = 400


class TileMap:
    """A read-only view of the world's tiles."""

    def __init__(self, mem, static_base: int):
        self.mem = mem
        self.buf = mem.read_u32(static_base + MAIN_TILE_OFF)
        b = mem.read_u32(self.buf + _BOUNDS_OFF) if self.buf else 0
        if not b:
            raise ValueError("Main.tile is not readable — is a world loaded?")
        self.stride = mem.read_i32(b + 0x08)          # buffer height, the index stride
        self.origin_x = mem.read_i32(b + 0x04)
        self.origin_y = mem.read_i32(b + 0x0C)
        self.max_x = mem.read_i32(static_base + MAIN_MAX_TILES_OFF)
        self.max_y = mem.read_i32(static_base + MAIN_MAX_TILES_OFF + 4)
        if not (self.stride and self.max_x and self.max_y):
            raise ValueError("world dimensions unreadable")

    def in_world(self, x: int, y: int) -> bool:
        return 0 <= x < self.max_x and 0 <= y < self.max_y

    def _entry(self, x: int, y: int) -> int:
        idx = self.stride * (x - self.origin_x) + (y - self.origin_y)
        return self.mem.read_u32(self.buf + _ENTRIES_OFF + 4 * idx) or 0

    def type_at(self, x: int, y: int) -> int | None:
        """The raw tile id at ``(x, y)``, or None outside the world / with no tile object.

        **This is not "what is there".** A mined-out tile keeps its id and only loses its
        active bit, so this reports "copper" for a vein someone already dug out. Use
        :meth:`solid_type_at` for anything that decides what to mine; this stays raw
        because the map view wants to show ids without a second read per tile.
        """
        if not self.in_world(x, y):
            return None
        p = self._entry(x, y)
        if not p:
            return None
        raw = self.mem.read(p + _TILE_TYPE_OFF, 2)
        if len(raw) < 2:
            return None
        return struct.unpack("<H", raw)[0]

    def active_at(self, x: int, y: int) -> bool | None:
        """Is there actually a tile at ``(x, y)``? None outside the world.

        ``Tile.active()`` is ``(sTileHeader & 32) == 32`` (verified against the game's own
        IL). Terraria clears that bit when a tile is mined but leaves ``type`` alone, so
        this is the only thing that separates "copper ore" from "where copper ore used to
        be". :meth:`check_active_offset` validates the field offset against the live game.
        """
        if not self.in_world(x, y):
            return None
        p = self._entry(x, y)
        if not p:
            return None
        raw = self.mem.read(p + _TILE_HEADER_OFF, 2)
        if len(raw) < 2:
            return None
        return bool(struct.unpack("<H", raw)[0] & _ACTIVE_BIT)

    def solid_type_at(self, x: int, y: int) -> int | None:
        """The tile id at ``(x, y)``, but only when a tile is really there.

        None for empty space, whatever stale id the entry still carries.
        """
        return self.type_at(x, y) if self.active_at(x, y) else None

    def check_active_offset(self, x: int, y: int) -> dict:
        """Sanity-check :data:`_TILE_HEADER_OFF` against the live world.

        The offset is derived from the field order, which mono is free to lay out as it
        likes, so it is checked rather than trusted: a column from the sky down to below
        the surface must be inactive at the top and active further down, and every id-0
        tile must be inactive. A wrong offset reads some other field and fails both.
        """
        col = [(y0, self.type_at(x, y0), self.active_at(x, y0))
               for y0 in range(max(0, y - 200), min(self.max_y, y + 60))]
        col = [c for c in col if c[2] is not None]
        sky = [c for c in col if c[1] == 0]
        bad_sky = [c for c in sky if c[2]]
        active = [c for c in col if c[2]]
        return {
            "sampled": len(col),
            "empty_ids": len(sky),
            "empty_but_active": len(bad_sky),      # must be 0: id 0 is never a real tile
            "active": len(active),
            "ok": bool(col) and not bad_sky and bool(active) and len(sky) > 0,
        }

    def column(self, x: int, y0: int, y1: int) -> list[int | None]:
        """Tile ids down one column, in a single read.

        The index is column-major, so a vertical run is contiguous in the pointer array —
        which makes scanning down far cheaper than scanning across.
        """
        y0, y1 = max(0, y0), min(self.max_y, y1)
        if not (0 <= x < self.max_x) or y1 <= y0:
            return []
        idx = self.stride * (x - self.origin_x) + (y0 - self.origin_y)
        blob = self.mem.read(self.buf + _ENTRIES_OFF + 4 * idx, 4 * (y1 - y0))
        out = []
        for i in range(y1 - y0):
            p = struct.unpack_from("<I", blob, 4 * i)[0] if len(blob) >= 4 * i + 4 else 0
            if not p:
                out.append(None)
                continue
            raw = self.mem.read(p + _TILE_TYPE_OFF, 2)
            out.append(struct.unpack("<H", raw)[0] if len(raw) == 2 else None)
        return out


def flood(tiles: TileMap, x: int, y: int, whitelist, limit: int = DEFAULT_LIMIT,
          diagonal: bool = True) -> list[tuple[int, int]]:
    """Every tile of the same id contiguous with ``(x, y)``, if that id is whitelisted.

    Matching on the **starting tile's own id** rather than on "any whitelisted id" is what
    stops a copper vein touching an iron one from taking both: a vein is one ore.

    Matching uses :meth:`~TileMap.solid_type_at`, so already-mined tiles do not join the
    vein. Matching on the raw id instead would hand the game coordinates for tiles that no
    longer exist -- harmless (``CanKillTile`` zeroes the damage) but pure wasted work, and
    it makes a vein look bigger than it is.

    Returns [] when the start is not whitelisted. The cap is a safety rail, not a
    performance one — a real vein is tens of tiles, and stopping early is far better than
    a mistake that strips a region.
    """
    want = tiles.solid_type_at(x, y)
    if want is None or want not in whitelist:
        return []
    steps = ((1, 0), (-1, 0), (0, 1), (0, -1))
    if diagonal:
        steps += ((1, 1), (1, -1), (-1, 1), (-1, -1))
    seen = {(x, y)}
    out = [(x, y)]
    queue = [(x, y)]
    while queue and len(out) < limit:
        cx, cy = queue.pop()
        for dx, dy in steps:
            n = (cx + dx, cy + dy)
            if n in seen:
                continue
            seen.add(n)
            if tiles.solid_type_at(*n) == want:
                out.append(n)
                queue.append(n)
                if len(out) >= limit:
                    break
    return out
