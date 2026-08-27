"""A modifier gives its effects, not just its name (spec 046).

Reported from the game: a Spider Staff set to Godly showed the name and had no bonuses.
The prefix byte is only what the tooltip reads; `Item.Prefix` multiplies the bonuses into
the item's own fields, and the trainer wrote the byte alone.
"""

import struct

import pytest

from terrariabonker import prefixes
from terrariabonker import service as S
from terrariabonker.inventory import (ARR_DATA_OFF, ARR_LEN_OFF, INVENTORY_PTR_OFF,
                                      ITEM_DAMAGE, ITEM_KNOCKBACK, ITEM_MANA, ITEM_PREFIX,
                                      ITEM_SCALE, ITEM_TYPE, ITEM_USE_ANIM, ITEM_USE_TIME,
                                      Inventory)

BASE = 0x80000000
LIFE = BASE + 0x6000
ARR = BASE + 0x100
ITEMS = BASE + 0x1000
STRIDE = 0x200

GODLY, LARGE, HARD, QUICK = 59, 1, 62, 42
SPIDER_STAFF = 3010
#: A pristine Spider Staff, as ContentSamples would hand it over.
BASE_STATS = {"damage": 9, "knockback": 2.2, "useanim": 25, "usetime": 25,
              "scale": 1.0, "shootspeed": 0.0, "mana": 10}


def _game(monkeypatch, stats=None):
    """A service over one inventory slot holding a Spider Staff, with a template to match."""
    from conftest import FakeMem

    stats = dict(stats or BASE_STATS)
    m = FakeMem(BASE, 0x20000)
    m.write(LIFE + INVENTORY_PTR_OFF, struct.pack("<I", ARR))
    m.poke_i32(ARR + ARR_LEN_OFF, 4)
    for i in range(4):
        addr = ITEMS + i * STRIDE
        m.write(ARR + ARR_DATA_OFF + i * 4, struct.pack("<I", addr))
    slot0 = ITEMS
    m.poke_i32(slot0 + ITEM_TYPE, SPIDER_STAFF)
    _write_stats(m, slot0, stats)

    inv = Inventory(m, LIFE)
    svc = S.Service.__new__(S.Service)
    svc.mem = m
    svc._live_inventory = lambda: inv
    svc._all_inventories = lambda: [inv]
    # The template block the service reads base stats out of: a pristine item's bytes.
    block = bytearray(S.ITEM_COPY_HI - S.ITEM_COPY_LO)
    for key, (off, kind) in Inventory.PREFIX_BASE_FIELDS.items():
        struct.pack_into("<i" if kind == "i32" else "<f", block, off - S.ITEM_COPY_LO,
                         stats[key])
    svc._template_block = lambda t: bytes(block)
    svc._place_item = lambda *a, **k: None
    return m, svc, inv


def _write_stats(m, addr, stats):
    m.poke_i32(addr + ITEM_DAMAGE, stats["damage"])
    m.write(addr + ITEM_KNOCKBACK, struct.pack("<f", stats["knockback"]))
    m.poke_i32(addr + ITEM_USE_ANIM, stats["useanim"])
    m.poke_i32(addr + ITEM_USE_TIME, stats["usetime"])
    m.write(addr + ITEM_SCALE, struct.pack("<f", stats["scale"]))
    m.poke_i32(addr + ITEM_MANA, stats["mana"])


def _read(m, key):
    off, kind = Inventory.PREFIX_BASE_FIELDS[key]
    raw = m.read(ITEMS + off, 4)
    return struct.unpack("<i" if kind == "i32" else "<f", raw)[0]


def test_the_reported_case_godly_gives_its_bonuses(monkeypatch):
    """Godly is damage x1.15 and knockback x1.15. It gave neither."""
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY)
    assert m.read(ITEMS + ITEM_PREFIX, 1)[0] == GODLY, "the name is still set"
    assert _read(m, "damage") == round(9 * 1.15)
    assert _read(m, "knockback") == pytest.approx(2.2 * 1.15, rel=1e-5)


def test_a_size_modifier_scales_the_size(monkeypatch):
    """Large touches nothing but scale, which is why it was entirely invisible before."""
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=LARGE)
    assert _read(m, "scale") == pytest.approx(1.12, rel=1e-5)
    assert _read(m, "damage") == 9, "Large must not touch damage"


def test_a_speed_modifier_scales_use_animation_and_use_time_from_their_own_bases():
    """One multiplier drives both fields, but each scales from ITS OWN base.

    They are equal on most weapons and not on all, so sharing one base value would quietly
    rewrite one of them to the other's number.
    """
    stats = dict(BASE_STATS, useanim=30, usetime=20)
    m, svc, _inv = _game(None, stats)
    mult = prefixes.stat_multipliers(QUICK).get("usetime")
    assert mult, "premise: this modifier changes speed"
    svc.set_item(0, SPIDER_STAFF, prefix=QUICK)
    assert _read(m, "useanim") == round(30 * mult)
    assert _read(m, "usetime") == round(20 * mult)


def test_applying_the_same_modifier_twice_does_not_compound(monkeypatch):
    """The bonuses multiply the PRISTINE item, so re-applying must be idempotent.

    Scaling the item's current values would give 1.15 x 1.15 the second time.
    """
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY)
    once = _read(m, "damage"), _read(m, "knockback")
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY)
    assert (_read(m, "damage"), _read(m, "knockback")) == once


def test_switching_modifiers_recomputes_from_base(monkeypatch):
    """B after A must equal B on a fresh item, not B on top of A."""
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY)
    svc.set_item(0, SPIDER_STAFF, prefix=LARGE)
    assert _read(m, "damage") == 9, "Godly's damage survived a switch to Large"
    assert _read(m, "scale") == pytest.approx(1.12, rel=1e-5)


def test_clearing_the_modifier_restores_the_base_stats(monkeypatch):
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY)
    svc.set_item(0, SPIDER_STAFF, prefix=0)
    assert _read(m, "damage") == 9
    assert _read(m, "knockback") == pytest.approx(2.2, rel=1e-5)
    assert m.read(ITEMS + ITEM_PREFIX, 1)[0] == 0


def test_an_explicit_damage_wins_over_the_modifier(monkeypatch):
    """The dialog's own numbers are applied after the modifier, so what you typed sticks."""
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=GODLY, damage=500)
    assert _read(m, "damage") == 500


def test_an_accessory_modifier_writes_only_the_byte(monkeypatch):
    """Not a gap: the game reads the byte in Player.GrantPrefixBenefits when the item is
    equipped, so accessory modifiers already worked and must not be 'fixed' into the
    item's own fields."""
    m, svc, _inv = _game(monkeypatch)
    svc.set_item(0, SPIDER_STAFF, prefix=HARD)
    assert m.read(ITEMS + ITEM_PREFIX, 1)[0] == HARD
    assert _read(m, "damage") == 9 and _read(m, "scale") == pytest.approx(1.0)


def test_crit_is_reported_as_skipped_rather_than_silently_dropped(monkeypatch):
    """crit/armorPen/tagDamage have unverified offsets, so they are not written.

    Named in the result instead: a modifier that quietly loses part of itself is the bug
    this spec exists to fix, and doing it a second time in the fix would be worse.
    """
    _m, svc, inv = _game(monkeypatch)
    got = svc._apply_prefix([inv], 0, SPIDER_STAFF, GODLY)
    assert "crit" in got["skipped"], got
    assert set(got["written"]) == {"damage", "knockback"}


def test_the_table_comes_from_the_game_and_covers_the_real_modifiers():
    """Extracted by tools/extract_prefix_stats.py, not transcribed."""
    assert prefixes.stat_multipliers(GODLY) == {"damage": 1.15, "knockback": 1.15,
                                                "crit": 5.0}
    assert prefixes.stat_multipliers(LARGE) == {"scale": 1.12}
    assert prefixes.stat_multipliers(HARD) == {}, "accessory modifiers change the player"
    assert len(prefixes._STAT_MULTIPLIERS) > 70


# --- the dialog must send edits, not echoes (the follow-up report) ------------

def test_the_dialog_sends_only_what_was_changed(qt_app, monkeypatch):
    """Reported: a modifier "only works the first time; afterwards it won't overwrite".

    The dialog opens showing the item's current stats and submitted all of them, so the
    item's own damage came back as an explicit edit -- which is applied AFTER the modifier
    and overwrote what the modifier computed. Only the fields the dialog does not carry
    (knockback, scale) ever survived, which is what made it look like a partial success.
    """
    from terrariabonker.gui.item_dialog import ItemEditDialog

    # `flags` is what tells the dialog this is a summon weapon and so takes modifiers;
    # without it the dropdown is (correctly) disabled and the test would prove nothing.
    row = {"slot": 0, "type": SPIDER_STAFF, "stack": 1, "damage": 26, "use_time": 36,
           "use_anim": 36, "pick": 0, "tile_boost": 0, "defense": 0, "prefix": 0,
           "flags": {"summon": True}}
    dlg = ItemEditDialog(None, row, [])
    try:
        dlg.prefix.setCurrentIndex(dlg.prefix.findData(GODLY))
        dlg._on_ok()
        assert dlg.changed == {"prefix"}, \
            f"the dialog is still echoing untouched fields: {dlg.changed}"
    finally:
        dlg.deleteLater()


def test_touching_a_field_still_sends_it(qt_app):
    from terrariabonker.gui.item_dialog import ItemEditDialog

    # `flags` is what tells the dialog this is a summon weapon and so takes modifiers;
    # without it the dropdown is (correctly) disabled and the test would prove nothing.
    row = {"slot": 0, "type": SPIDER_STAFF, "stack": 1, "damage": 26, "use_time": 36,
           "use_anim": 36, "pick": 0, "tile_boost": 0, "defense": 0, "prefix": 0,
           "flags": {"summon": True}}
    dlg = ItemEditDialog(None, row, [])
    try:
        dlg.damage.setValue(500)
        dlg.prefix.setCurrentIndex(dlg.prefix.findData(GODLY))
        dlg._on_ok()
        assert dlg.changed == {"damage", "prefix"}
    finally:
        dlg.deleteLater()


def test_a_submission_that_changed_nothing_writes_nothing(qt_app, monkeypatch, gui_window):
    """Opening the dialog and pressing OK must not rewrite the slot.

    Harmless-looking, and it was not: every field the item already had was sent back as an
    edit, which is the mechanism behind the reported bug.
    """
    w = gui_window()
    try:
        sent = []
        monkeypatch.setattr(w, "_write_slot", lambda argv: sent.append(argv))
        row = {"type": SPIDER_STAFF, "stack": 1, "damage": 26, "prefix": GODLY}
        w._apply_item_edit(0, row, SPIDER_STAFF, changed=set())
        assert sent == [], "an untouched dialog still wrote to the slot"
    finally:
        w.close()
