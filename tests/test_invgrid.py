"""Tests for the Qt-free grid-inventory helpers (no display needed)."""

import unittest

from terrariabonker.gui import invgrid


class SectionTests(unittest.TestCase):
    def test_slot_to_section(self):
        self.assertEqual(invgrid.section_of(0), "Hotbar")
        self.assertEqual(invgrid.section_of(9), "Hotbar")
        self.assertEqual(invgrid.section_of(10), "Inventory")
        self.assertEqual(invgrid.section_of(49), "Inventory")
        self.assertEqual(invgrid.section_of(50), "Coins")
        self.assertEqual(invgrid.section_of(53), "Coins")
        self.assertEqual(invgrid.section_of(54), "Ammo")
        self.assertEqual(invgrid.section_of(57), "Ammo")

    def test_slot_58_is_hidden(self):
        self.assertIsNone(invgrid.section_of(58))
        self.assertNotIn(58, invgrid.GRID_SLOTS)

    def test_grid_covers_0_through_57(self):
        self.assertEqual(invgrid.GRID_SLOTS, list(range(0, 58)))


class LabelTests(unittest.TestCase):
    def test_short_name_passes_through(self):
        self.assertEqual(invgrid.abbrev("Dirt"), "Dirt")

    def test_multiword_name_abbreviates(self):
        out = invgrid.abbrev("Copper Pickaxe")
        self.assertIn(".", out)
        self.assertLess(len(out), len("Copper Pickaxe"))

    def test_long_single_word_truncates_with_ellipsis(self):
        out = invgrid.abbrev("Supercalifragilistic")
        self.assertTrue(out.endswith("…"))
        self.assertLessEqual(len(out), 9)

    def test_stack_badge(self):
        self.assertEqual(invgrid.stack_badge(1), "")
        self.assertEqual(invgrid.stack_badge(0), "")
        self.assertEqual(invgrid.stack_badge(99), "99")


class StateTests(unittest.TestCase):
    def test_is_empty(self):
        self.assertTrue(invgrid.is_empty({}))
        self.assertTrue(invgrid.is_empty({"type": 0}))
        self.assertFalse(invgrid.is_empty({"type": 5}))

    def test_tooltip_empty(self):
        tip = invgrid.tooltip({"slot": 12, "type": 0}, "")
        self.assertIn("empty", tip.lower())
        self.assertIn("12", tip)

    def test_tooltip_filled_shows_mapped_fields(self):
        row = {"slot": 0, "type": 3509, "stack": 1, "damage": 4,
               "pick": 35, "tile_boost": 5, "auto_reuse": 1, "use_time": 15}
        tip = invgrid.tooltip(row, "Copper Pickaxe")
        self.assertIn("Copper Pickaxe", tip)
        self.assertIn("#3509", tip)
        self.assertIn("Damage 4", tip)
        self.assertIn("35%", tip)
        self.assertIn("+5", tip)

    def test_tooltip_hides_inapplicable_fields(self):
        # An accessory-like item: no damage, no pick power, no reach.
        row = {"slot": 20, "type": 100, "stack": 1, "damage": -1,
               "pick": 0, "tile_boost": 0, "auto_reuse": 0, "use_time": 20}
        tip = invgrid.tooltip(row, "Shadow Greaves")
        self.assertNotIn("Damage", tip)
        self.assertNotIn("Pickaxe power", tip)
        self.assertNotIn("Placement reach", tip)


if __name__ == "__main__":
    unittest.main()
