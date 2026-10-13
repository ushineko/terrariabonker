package gui

import (
	"strconv"
	"strings"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/gui/client"
)

/*
What the item editor decides, kept apart from the widgets that collect it so it
can be tested without a display: which fields a set of typed values turns into a
write, which modifiers an item is offered, and what a typed item resolves to.
*/

// itemForm is what the editor's fields hold, as numbers.
type itemForm struct {
	Type      int
	Stack     int
	Damage    int
	Defense   int
	Prefix    int
	AutoReuse bool
	UseTime   int
	UseAnim   int
	Pick      int
	TileBoost int
}

// formOf is the form a slot opens with. An empty slot opens with a stack of
// one, because that is what placing something there means.
func formOf(s client.ItemSlot) itemForm {
	return itemForm{
		Type:      s.Type,
		Stack:     max(s.Stack, 1),
		Damage:    s.Damage,
		Defense:   s.Defense,
		Prefix:    s.Prefix,
		AutoReuse: s.AutoReuse != 0,
		UseTime:   s.UseTime,
		UseAnim:   s.UseAnim,
		Pick:      s.Pick,
		TileBoost: s.TileBoost,
	}
}

/*
editOf is the write that turns a slot into what the form now says, and whether
there is anything to write at all.

Only the fields that were changed are sent. The form opens showing the item's
current stats, so sending them all back submits the item's own damage as an
explicit edit -- which lands after a modifier is applied and overwrites what the
modifier computed (spec 046) -- and re-applies an unchanged modifier, which
resets the item to its base stats first.

A different item is sent as its type and stack alone, so the game's own
template supplies its stats: the numbers in the form belong to the item that
was there.
*/
func editOf(s client.ItemSlot, now itemForm) (client.ItemEdit, bool) {
	var e client.ItemEdit
	if !s.Empty() {
		had := s.Type
		e.ExpectType = &had
	}
	if now.Type != s.Type {
		if now.Type != 0 {
			e.Stack = &now.Stack
		}
		return e, true
	}
	if s.Empty() {
		return e, false
	}

	was := formOf(s)
	changed := false
	set := func(dst **int, before, after int) {
		if before != after {
			*dst = &after
			changed = true
		}
	}
	set(&e.Stack, was.Stack, now.Stack)
	set(&e.Damage, was.Damage, now.Damage)
	set(&e.Defense, was.Defense, now.Defense)
	set(&e.Prefix, was.Prefix, now.Prefix)
	set(&e.AutoReuse, flag(was.AutoReuse), flag(now.AutoReuse))
	set(&e.UseTime, was.UseTime, now.UseTime)
	set(&e.UseAnim, was.UseAnim, now.UseAnim)
	set(&e.Pick, was.Pick, now.Pick)
	set(&e.TileBoost, was.TileBoost, now.TileBoost)
	return e, changed
}

// flag is a switch as the number the CLI takes.
func flag(on bool) int {
	if on {
		return 1
	}
	return 0
}

/*
prefixChoices is the modifiers the editor offers for a slot, in dropdown order.

An item's damage class decides what it can roll, so a sword is not offered
Warding. An empty slot has no class yet and is offered everything; an item that
is neither a weapon nor an accessory is offered nothing. A modifier the item
already carries is always in the list, whatever the pools say, so opening the
editor never shows "none" for an item that has one.
*/
func prefixChoices(all *game.Prefixes, s client.ItemSlot) []int {
	flags := make(map[string]bool, len(s.Flags))
	for name, v := range s.Flags {
		if on, ok := v.(bool); ok {
			flags[name] = on
		}
	}
	var ids []int
	switch {
	case s.Empty():
		ids = all.All()
	case game.HasCategories(flags):
		ids = all.Valid(flags)
	}
	if s.Prefix == 0 {
		return ids
	}
	for _, id := range ids {
		if id == s.Prefix {
			return ids
		}
	}
	return append([]int{s.Prefix}, ids...)
}

/*
prefixLabels names each choice for the dropdown, one label per id.

Several modifiers share a name -- there are two Deadlys and two Hastys, one per
damage class -- and a dropdown that reads its answer back by label would give
the first of each for both. A repeated label carries its id.
*/
func prefixLabels(ids []int, known map[int]client.Prefix) []string {
	labels := make([]string, len(ids))
	seen := map[string]int{}
	for i, id := range ids {
		p, ok := known[id]
		if !ok || p.Name == "" {
			labels[i] = "#" + strconv.Itoa(id)
		} else {
			labels[i] = p.Name + " (" + p.Quality + ")"
		}
		seen[labels[i]]++
	}
	for i, id := range ids {
		if seen[labels[i]] > 1 {
			labels[i] += " #" + strconv.Itoa(id)
		}
	}
	return labels
}

/*
resolveItem is the item a typed name or number means.

A number is an item id. Anything else is searched for among the names, and the
shortest name containing it wins, which is what the search is ordered by: "wood"
is Wood, not Rich Mahogany Work Bench. Zero is "nothing", which empties a slot.
*/
func resolveItem(names *game.Names, text string) (int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(text); err == nil {
		return n, n >= 0
	}
	if names == nil {
		return 0, false
	}
	if hits := names.Search(text, 1); len(hits) == 1 {
		return hits[0].ID, true
	}
	return 0, false
}
