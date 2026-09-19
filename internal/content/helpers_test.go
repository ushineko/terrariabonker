package content_test

import (
	"encoding/json"
	"sort"

	"github.com/ushineko/terrariabonker/internal/content"
)

// sortedFields is a planted object's field names in order, so both sides write
// them in the same sequence.
func sortedFields(fields map[string]int32) []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pyJSON is a Go value as a Python literal, via JSON, which the two agree on
// except for the three words spelled differently.
func pyJSON(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	for from, to := range map[string]string{"true": "True", "false": "False", "null": "None"} {
		s = replaceWord(s, from, to)
	}
	return s
}

// replaceWord swaps whole words only, so a value containing one is left alone.
func replaceWord(s, from, to string) string {
	var out []byte
	for i := 0; i < len(s); {
		if i+len(from) <= len(s) && s[i:i+len(from)] == from &&
			(i == 0 || !isWord(s[i-1])) &&
			(i+len(from) == len(s) || !isWord(s[i+len(from)])) {
			out = append(out, to...)
			i += len(from)
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

func isWord(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// statsFrom builds an item's stats from a sparse case, with the defaults a real
// template carries for the fields the case leaves out.
func statsFrom(c map[string]any) content.ItemStats {
	s := content.ItemStats{HeadSlot: -1, BodySlot: -1, LegSlot: -1, CreateTile: -1}
	set := func(name string, into *int32) {
		if v, ok := c[name]; ok {
			*into = int32(v.(int))
		}
	}
	flag := func(name string, into *bool) {
		if v, ok := c[name]; ok {
			*into = v.(bool)
		}
	}
	set("damage", &s.Damage)
	set("defense", &s.Defense)
	set("pick", &s.Pick)
	set("head_slot", &s.HeadSlot)
	set("body_slot", &s.BodySlot)
	set("leg_slot", &s.LegSlot)
	set("create_tile", &s.CreateTile)
	set("heal_life", &s.HealLife)
	set("heal_mana", &s.HealMana)
	set("buff_type", &s.BuffType)
	flag("accessory", &s.Accessory)
	flag("melee", &s.Melee)
	flag("ranged", &s.Ranged)
	flag("magic", &s.Magic)
	flag("summon", &s.Summon)
	return s
}

// npcStatsFrom is the same for an NPC.
func npcStatsFrom(c map[string]any) content.NPCStats {
	var s content.NPCStats
	if v, ok := c["damage"]; ok {
		s.Damage = int32(v.(int))
	}
	if v, ok := c["boss"]; ok {
		s.Boss = v.(bool)
	}
	if v, ok := c["town"]; ok {
		s.Town = v.(bool)
	}
	return s
}
