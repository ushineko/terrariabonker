/*
Package client is the one place that knows the CLI contract the GUI depends on.

The GUI cannot call the common layer in-process (it is unprivileged; memory access
needs root), so it reaches the same operations across a sudo subprocess boundary.
This package is the single definition of that boundary: each function returns the
CLI argv for an operation, and the parsers decode the --json replies. It is
deliberately toolkit-free — it imports no Fyne and nothing from the window — so
the parity test can check every command it emits against the real CLI parser,
turning a contract drift (a missing --json, a renamed subcommand) into a test
failure instead of a runtime gap.

This is the Go counterpart of terrariabonker/gui/client.py, and the two must say
the same thing for as long as both exist. See spec 050.
*/
package client

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Status is what `status --json` reports: who the player is, what they are made
// of, and which build and world this is. Every field is optional on the wire —
// the game may be running with no player loaded — so the pointers distinguish
// "absent" from "zero", which matters for HP.
type Status struct {
	PID         int     `json:"pid"`
	Version     string  `json:"version"`
	CompatLevel int     `json:"compat_level"`
	BuildID     string  `json:"buildid"`
	Build       string  `json:"build"`
	Copies      int     `json:"copies"`
	Name        *string `json:"name"`
	HP          *int    `json:"hp"`
	MaxHP       *int    `json:"max_hp"`
	Mana        *int    `json:"mana"`
	MaxMana     *int    `json:"max_mana"`
	// World is which world is loaded, so the panel can re-apply the profile on a
	// world switch and not only on a new pid (spec 049). Null when unreadable.
	World json.RawMessage `json:"world"`
}

// --- read operations: argv and parser ---------------------------------------

// StatusArgv asks what is running and who is in it.
func StatusArgv() []string { return []string{"status", "--json"} }

// ParseStatus decodes a status reply. The CLI may print progress or warnings
// before its JSON, so the last line is the document — the same rule client.py
// applies, and the reason it exists is that a warning on stdout must not make a
// reply unreadable.
func ParseStatus(raw string) (*Status, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var s Status
	if err := json.Unmarshal([]byte(line), &s); err != nil {
		return nil, false
	}
	return &s, true
}

// InventoryArgv asks for every slot, not only the occupied ones: an empty slot
// is a thing the grid draws.
func InventoryArgv() []string { return []string{"inventory", "--all", "--json"} }

// ParseInventory decodes an inventory reply into raw rows. The window's own
// types live above this; keeping them out of the contract means a new column in
// the CLI's output does not break the parse.
func ParseInventory(raw string) ([]map[string]any, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(line), &rows); err != nil {
		return nil, false
	}
	return rows, true
}

// --- mutation operations: argv builders -------------------------------------

// SetHPArgv sets current HP. The value is a string because "max" is a valid
// setting and is what the Heal button sends.
func SetHPArgv(value string) []string { return []string{"set-hp", value} }

// SetManaArgv sets current mana; "max" refills.
func SetManaArgv(value string) []string { return []string{"set-mana", value} }

// SetMaxHPArgv raises or lowers the ceiling.
func SetMaxHPArgv(value int) []string {
	return []string{"set-max-hp", strconv.Itoa(value)}
}

// SetMaxManaArgv raises or lowers the mana ceiling.
func SetMaxManaArgv(value int) []string {
	return []string{"set-max-mana", strconv.Itoa(value)}
}

// FastMiningArgv makes every pickaxe fast.
func FastMiningArgv() []string { return []string{"fast-mining"} }

// LongReachArgv extends block reach by n tiles.
func LongReachArgv(tiles int) []string {
	return []string{"long-reach", strconv.Itoa(tiles)}
}

// VersionArgv is what the CLI reports about itself; used to check that the
// binary the window found is the one it expects.
func VersionArgv() []string { return []string{"version", "--json"} }

// lastLine returns the final non-empty line of a reply.
func lastLine(raw string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s, true
		}
	}
	return "", false
}
