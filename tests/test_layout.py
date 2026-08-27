"""One home for the memory layout (mid-project review §2.3).

Every number in `layout` is build-specific and has to be re-derived when Terraria updates.
That job used to start with "find every spelling of this constant": the mono szarray shape
was declared five times under four names, and Main's static offsets were spread across five
modules. These tests fail if a module goes back to declaring its own copy.
"""

from terrariabonker import inventory, layout, locate, npcs, projectiles, recipes, tiles


def _assigned_from_layout(mod, name: str) -> bool:
    """Is `name` assigned from `layout.<something>` at module level, rather than a literal?

    Comparing values cannot answer this: `0x10 is layout.ARR_DATA_OFF` is True because
    CPython interns small ints, so an identity check passes against a module that went
    back to declaring its own copy. The first version of these tests did exactly that and
    a mutation walked straight through it.
    """
    import ast
    from pathlib import Path

    tree = ast.parse(Path(mod.__file__).read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and any(
                isinstance(t, ast.Name) and t.id == name for t in node.targets):
            v = node.value
            return isinstance(v, ast.Attribute) and isinstance(v.value, ast.Name) \
                and v.value.id == "layout"
    return False


def test_every_module_takes_its_szarray_offsets_from_layout():
    """Four names for two numbers. The names stay (imports depend on them); the values
    must come from one place, so re-deriving is a single edit."""
    for mod, names in ((inventory, ("ARR_LEN_OFF", "ARR_DATA_OFF")),
                       (projectiles, ("ARRAY_LEN_OFF", "ARRAY_DATA_OFF")),
                       (recipes, ("ARR_LEN", "ARR_DATA")),
                       (tiles, ("_ENTRIES_OFF",))):
        for name in names:
            assert _assigned_from_layout(mod, name), \
                f"{mod.__name__}.{name} re-declares the offset instead of importing it"


def test_the_length_is_at_0x0c_not_0x08():
    """The trap this cost an afternoon to: a scan for the projectile array using +0x08
    found no array at all and read as "the structure is not there"."""
    assert layout.ARR_LEN_OFF == 0x0C
    assert layout.ARR_DATA_OFF == 0x10


def test_every_main_static_offset_comes_from_layout():
    """They are all offsets into the same block, reached by locate.main_static_base."""
    for mod, name in ((locate, "MAIN_PLAYER_OFF"), (recipes, "MAIN_RECIPE_OFF"),
                      (tiles, "MAIN_TILE_OFF"), (tiles, "MAIN_MAX_TILES_OFF"),
                      (npcs, "MAIN_NPC_OFF"), (npcs, "MAIN_NPC_FRAME_COUNT_OFF"),
                      (projectiles, "MAIN_PROJECTILE_OFF")):
        assert _assigned_from_layout(mod, name), \
            f"{mod.__name__}.{name} is declared locally, not in layout"


def test_item_offsets_are_not_re_declared_by_recipes():
    """recipes carried its own copies of ITEM_TYPE and ITEM_STACK, so an Item layout
    change had two places to be applied and no way to notice if only one was."""
    import ast
    from pathlib import Path

    tree = ast.parse(Path(recipes.__file__).read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and any(
                isinstance(t, ast.Name) and t.id in ("ITEM_TYPE", "ITEM_STACK")
                for t in node.targets):
            v = node.value
            assert isinstance(v, ast.Attribute) and getattr(v.value, "id", "") == "inventory", \
                "recipes re-declares an Item offset instead of importing it"


# There is deliberately NO test here for "no module adds a bare 0x10". It was written and
# removed: as an AST walk for the literal in an addition it flagged `mem.read(ptr + 12, ...)`
# reading a mono String's characters and a tile-buffer origin at `+0x0C` -- same numbers,
# unrelated meanings. A rule that fires on those would be switched off the first time it
# did. The identity checks above are the real guarantee: the named constants all resolve to
# `layout`, so re-deriving one is one edit.
