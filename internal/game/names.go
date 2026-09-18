/*
Package game is the game's own tables, read in this process.

The window used to ask the CLI for these -- four subprocesses at start-up, for
item names, modifiers, recipes and the patch catalog -- because the tables lived
in Python and an unprivileged front end in another language could not reach
them. They are JSON extracted from Terraria's own files (see the data package),
so there is nothing to reach across a process boundary for: Go reads the same
bytes.

This is step 1 of spec 051. Nothing here touches memory, needs privilege or
needs a running game, which is what makes it the place to start: every table is
checked row for row against what the Python makes of the same file, and that
habit is what the rest of the port depends on.
*/
package game

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ushineko/terrariabonker/data"
)

// Names is the ItemID to display-name table, and the tooltips beside it.
type Names struct {
	byID     map[int]string
	tooltips map[int]string
}

// names is loaded once: the table is 6,195 rows of constants and every caller
// wants the same ones.
var (
	namesOnce sync.Once
	loaded    *Names
	namesErr  error
)

/*
ItemNames is the table, loaded on the first call.

An error only if the bundled data is unreadable, which means the binary was
built wrong rather than that the machine is missing something -- the files are
embedded, not installed.
*/
func ItemNames() (*Names, error) {
	namesOnce.Do(func() {
		byID, err := intKeyed(data.Items)
		if err != nil {
			namesErr = err
			return
		}
		tips, err := intKeyed(data.Tooltips)
		if err != nil {
			namesErr = err
			return
		}
		loaded = &Names{byID: byID, tooltips: tips}
	})
	return loaded, namesErr
}

// intKeyed reads one of the tables whose keys are numbers written as strings,
// which is what JSON objects can hold.
func intKeyed(file string) (map[int]string, error) {
	raw, err := data.FS.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var byText map[string]string
	if err := json.Unmarshal(raw, &byText); err != nil {
		return nil, fmt.Errorf("decode %s: %w", file, err)
	}
	out := make(map[int]string, len(byText))
	for key, value := range byText {
		id, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an item id: %w", file, key, err)
		}
		out[id] = value
	}
	return out, nil
}

// Name is an item's display name, or "" for an id the table does not have --
// an item from a version this table was not extracted from.
func (n *Names) Name(id int) string { return n.byID[id] }

/*
Label is a name that is never empty: the item's own, or "#5400" for one the
table does not know, or "(empty)" for the zero id.

Zero is the game's own way of saying a slot holds nothing, so it is not an
unknown item; it is no item.
*/
func (n *Names) Label(id int) string {
	if name := n.byID[id]; name != "" {
		return name
	}
	if id == 0 {
		return "(empty)"
	}
	return "#" + strconv.Itoa(id)
}

// Tooltip is the game's own tooltip for an item, or "" when it has none.
func (n *Names) Tooltip(id int) string { return n.tooltips[id] }

// All is a copy of the table, for a caller that wants to hold it.
func (n *Names) All() map[int]string {
	out := make(map[int]string, len(n.byID))
	for id, name := range n.byID {
		out[id] = name
	}
	return out
}

// Len is how many items the table names.
func (n *Names) Len() int { return len(n.byID) }

/*
Search is the items whose name contains query, shortest first.

Shortest first because a short name containing the query is usually the thing
being looked for: "Wood" before "Rich Mahogany Work Bench" for "wood". Ties go
to the lower id, so the order is the same on every run.
*/
func (n *Names) Search(query string, limit int) []Match {
	want := strings.ToLower(strings.TrimSpace(query))
	if want == "" {
		return nil
	}
	var hits []Match
	for id, name := range n.byID {
		if strings.Contains(strings.ToLower(name), want) {
			hits = append(hits, Match{ID: id, Name: name})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].Name) != len(hits[j].Name) {
			return len(hits[i].Name) < len(hits[j].Name)
		}
		return hits[i].ID < hits[j].ID
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Match is one search hit.
type Match struct {
	ID   int
	Name string
}
