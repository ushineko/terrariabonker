package game

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ushineko/terrariabonker/data"
)

/*
Prefixes is the modifier table: what each is called, what it does to an item,
and which items can roll it.

The names and the stat multipliers are extracted data (prefixes.json,
prefix_stats.json). The pools and the good/bad split below are not: they are
Terraria's own categorisation, written down by hand on the Python side and
written down again here.

That is the failure mode this project has already paid for once -- a constant
spelled twice drifts -- so it is not left to inspection. Every id's name and
quality, every class combination's pool, and every multiplier are compared with
the Python's answer while both exist.
*/
type Prefixes struct {
	names map[int]string
	stats map[int]map[string]float64
}

var (
	prefixOnce sync.Once
	prefixes   *Prefixes
	prefixErr  error
)

// ItemPrefixes is the modifier table, loaded on the first call.
func ItemPrefixes() (*Prefixes, error) {
	prefixOnce.Do(func() {
		names, err := intKeyed(data.Prefixes)
		if err != nil {
			prefixErr = err
			return
		}
		stats, err := prefixStats()
		if err != nil {
			prefixErr = err
			return
		}
		prefixes = &Prefixes{names: names, stats: stats}
	})
	return prefixes, prefixErr
}

// prefixStats reads what each modifier multiplies into the item.
func prefixStats() (map[int]map[string]float64, error) {
	raw, err := data.FS.ReadFile(data.PrefixStats)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", data.PrefixStats, err)
	}
	var byText map[string]map[string]float64
	if err := json.Unmarshal(raw, &byText); err != nil {
		return nil, fmt.Errorf("decode %s: %w", data.PrefixStats, err)
	}
	out := make(map[int]map[string]float64, len(byText))
	for key, fields := range byText {
		id, err := atoiKey(data.PrefixStats, key)
		if err != nil {
			return nil, err
		}
		out[id] = fields
	}
	return out, nil
}

// The quality of a modifier, for the dot beside an item.
const (
	QualityNone    = "none"
	QualityGood    = "good"
	QualityBad     = "bad"
	QualityNeutral = "neutral"
)

// ClassFlags are the item classes a modifier pool is chosen by.
var ClassFlags = []string{"melee", "ranged", "magic", "summon", "accessory"}

/*
Terraria's own modifier categorisation.

universal is what every weapon can roll; each class adds its own pool, which for
melee is the size modifiers, for ranged and magic their own, and for each a
top-tier of its own -- Legendary, Unreal, Mythical. Accessories roll only
accessory modifiers and nothing else.

bad and neutral name the modifiers that are not an improvement; everything else
that exists is. A list rather than a rule because the game has no rule: it is a
table of what each modifier does, and these are the ones whose effect is
negative or nil.
*/
var (
	universal = idRange(36, 62)
	classPool = map[string][]int{
		"melee":     append(idRange(1, 16), 81),
		"ranged":    append(idRange(16, 26), 82),
		"magic":     append(idRange(26, 36), 83),
		"summon":    idRange(84, 98),
		"accessory": idRange(62, 81),
	}
	bad = ids(7, 8, 9, 10, 11, 13, 22, 23, 24, 29, 30, 31, 39, 40, 41, 47, 48, 49, 50,
		55, 56, 91, 92, 93, 94, 97)
	neutral = ids(12, 14, 15)
)

// AdditiveStats are the fields a modifier adds to rather than multiplies into.
var AdditiveStats = map[string]bool{"crit": true, "tagdamage": true, "armorpen": true}

// idRange is the ids from lo up to but not including hi, which is how the
// Python spells these pools.
func idRange(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for id := lo; id < hi; id++ {
		out = append(out, id)
	}
	return out
}

// ids is a set of the listed numbers.
func ids(list ...int) map[int]bool {
	out := make(map[int]bool, len(list))
	for _, id := range list {
		out[id] = true
	}
	return out
}

// Name is a modifier's readable name, or "" for none and for an id the table
// does not have.
func (p *Prefixes) Name(id int) string {
	if id == 0 {
		return ""
	}
	return p.names[id]
}

// Quality is what the dot beside an item should say: good, bad, neutral, or
// none for an item with no modifier at all.
func (p *Prefixes) Quality(id int) string {
	switch {
	case id == 0:
		return QualityNone
	case bad[id]:
		return QualityBad
	case neutral[id]:
		return QualityNeutral
	}
	return QualityGood
}

// Stats is what a modifier multiplies into the item's own fields, or an empty
// map for one that does nothing to the item.
//
// Empty is a real answer rather than a gap: the accessory modifiers change the
// player when the item is equipped, so there is nothing to write into the item.
func (p *Prefixes) Stats(id int) map[string]float64 {
	out := make(map[string]float64, len(p.stats[id]))
	for field, value := range p.stats[id] {
		out[field] = value
	}
	return out
}

/*
Valid is the modifiers an item with these class flags can roll, by name.

An accessory rolls the accessory pool alone. A weapon rolls the universal set
plus the pool of every damage class it is, because an item can be more than one.
Anything that is neither rolls nothing.
*/
func (p *Prefixes) Valid(flags map[string]bool) []int {
	pool := map[int]bool{}
	if flags["accessory"] {
		for _, id := range classPool["accessory"] {
			pool[id] = true
		}
	} else {
		for _, class := range []string{"melee", "ranged", "magic", "summon"} {
			if !flags[class] {
				continue
			}
			for _, id := range universal {
				pool[id] = true
			}
			for _, id := range classPool[class] {
				pool[id] = true
			}
		}
	}
	return p.byName(pool)
}

// HasCategories reports whether an item can take a modifier at all: a weapon or
// an accessory can, and nothing else can.
func HasCategories(flags map[string]bool) bool {
	for _, class := range ClassFlags {
		if flags[class] {
			return true
		}
	}
	return false
}

// All is every modifier id, by name, for a picker with no item to narrow it.
func (p *Prefixes) All() []int {
	pool := make(map[int]bool, len(p.names))
	for id := range p.names {
		pool[id] = true
	}
	return p.byName(pool)
}

// byName sorts a pool the way a dropdown reads it. Ties go to the lower id, so
// two modifiers of the same name keep one order between runs.
func (p *Prefixes) byName(pool map[int]bool) []int {
	out := make([]int, 0, len(pool))
	for id := range pool {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(p.names[out[i]]), strings.ToLower(p.names[out[j]])
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}
