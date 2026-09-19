package game

import (
	"strconv"
	"sync"

	"github.com/ushineko/terrariabonker/data"
)

// NPCNames is the NPCID to display-name table.
type NPCNames struct{ byID map[int]string }

// npcNames is loaded once: every caller wants the same rows.
var (
	npcOnce   sync.Once
	npcLoaded *NPCNames
	npcErr    error
)

/*
NPCs is the table, loaded on the first call.

Keyed on the net id rather than the type, like the game's own collection: the
variants share a type and are told apart only by a negative net id, which is why
those ids appear here at all.
*/
func NPCs() (*NPCNames, error) {
	npcOnce.Do(func() {
		byID, err := intKeyed(data.NPCs)
		if err != nil {
			npcErr = err
			return
		}
		npcLoaded = &NPCNames{byID: byID}
	})
	return npcLoaded, npcErr
}

// Name is an NPC's display name, or nothing when the table does not have it.
func (n *NPCNames) Name(id int) string { return n.byID[id] }

/*
Label is a name that is never empty.

An id nobody has a name for still has to appear in a list, and "#123" is an
answer somebody can act on where an empty cell is not.
*/
func (n *NPCNames) Label(id int) string {
	if name := n.byID[id]; name != "" {
		return name
	}
	return "#" + strconv.Itoa(id)
}

// All is every id and name.
func (n *NPCNames) All() map[int]string {
	out := make(map[int]string, len(n.byID))
	for id, name := range n.byID {
		out[id] = name
	}
	return out
}

// Count is how many names there are.
func (n *NPCNames) Count() int { return len(n.byID) }
