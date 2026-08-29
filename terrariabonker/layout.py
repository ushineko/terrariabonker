"""One home for the game's memory layout: mono's array shape and Main's static offsets.

Every number here is build-specific and has to be re-derived when the game updates, which
is the routine event this project is built around. Before this module existed the mono
szarray layout was declared five times under four different names -- ``ARR_LEN_OFF`` /
``ARR_DATA_OFF`` in ``inventory``, ``ARRAY_LEN_OFF`` / ``ARRAY_DATA_OFF`` in
``projectiles``, ``ARR_LEN`` / ``ARR_DATA`` in ``recipes``, ``_ENTRIES_OFF`` in ``tiles``,
and a bare ``0x10`` inline in ``locate`` -- so re-deriving one number meant finding every
spelling of it first.

The `Main` static offsets were likewise spread across five modules despite all being
offsets into the same block, reachable with ``locate.main_static_base``.

See ``docs/discovery.md`` for how these were found, and the build ledger in ``patcher``
for which builds each one has been seen working on.
"""

from __future__ import annotations

# --- mono szarray (32-bit) --------------------------------------------------
# An array object is: vtable, sync block, bounds pointer, then the length; elements start
# at +0x10. The length sits at +0x0C, NOT +0x08 -- a first pass at the projectile array
# used +0x08 and concluded the structure was not there at all.
ARR_LEN_OFF = 0x0C              # max_length
ARR_DATA_OFF = 0x10             # first element

# --- Terraria.Main static-data block ----------------------------------------
# Offsets from the base that ``locate.main_static_base`` resolves.
MAIN_MAX_TILES_OFF = 0x5A4      # maxTilesX, then maxTilesY
MAIN_TILE_OFF = 0x99C           # Main.tile
MAIN_NPC_OFF = 0x9B0            # Main.npc
MAIN_PROJECTILE_OFF = 0x9BC     # Main.projectile
MAIN_RECIPE_OFF = 0xA68         # Main.recipe
MAIN_PLAYER_OFF = 0xA7C         # Main.player
MAIN_NPC_FRAME_COUNT_OFF = 0xC34  # Main.npcFrameCount
# Main.worldName, a mono string, and the only thing in this block that identifies which
# world is loaded. Found by diffing the static block across a real world switch (spec 049):
# 139 dwords changed and this was the one whose value matched the world files either side.
# The tile buffer's address and the world dimensions do NOT identify a world -- both were
# byte-identical across a switch between two 4200x1200 worlds.
MAIN_WORLD_NAME_OFF = 0x660
