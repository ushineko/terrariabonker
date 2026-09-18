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

// LongReachArgv extends placement reach by n tiles. The count is a flag, not a
// positional: `long-reach 20` does not parse.
func LongReachArgv(tiles int) []string {
	return []string{"long-reach", "--tiles", strconv.Itoa(tiles)}
}

// --- the trainer-held watches ------------------------------------------------
//
// None of these is the CLI's own --watch form. The worker must not block, so
// the window owns the cadence and sends one round per tick.

// PotionsArgv is one renewal round for favorited potions.
func PotionsArgv(minStack int) []string {
	return []string{"potions", "--json", "--min-stack", strconv.Itoa(minStack)}
}

// FishingArgv is one bait round: hand out the kit if asked, then top bait up.
func FishingArgv(keep int, kit bool) []string {
	argv := []string{"fishing", "--json", "--keep", strconv.Itoa(keep)}
	if !kit {
		argv = append(argv, "--no-kit")
	}
	return argv
}

// FishingPowerArgv raises every rod the player carries to power.
func FishingPowerArgv(power int) []string {
	return []string{"fishing", "--json", "--no-kit", "--power", strconv.Itoa(power)}
}

// FishingRestoreArgv puts the rods back to the power they had. Rod power is the
// one thing on the Effects section written into the save, so switching the watch
// off restores it instead of leaving a permanent change behind.
func FishingRestoreArgv() []string {
	return []string{"fishing", "--json", "--no-kit", "--restore"}
}

// FishingBuffsArgv is one round of holding the fishing potion effects up.
func FishingBuffsArgv(power, sonar, crate bool) []string {
	argv := []string{"fishing-buffs", "--json"}
	for _, f := range []struct {
		on   bool
		flag string
	}{{power, "--power"}, {sonar, "--sonar"}, {crate, "--crate"}} {
		if f.on {
			argv = append(argv, f.flag)
		}
	}
	return argv
}

// CatchArgv is one slice of auto-catch.
func CatchArgv(recast bool) []string {
	argv := []string{"catch-tick", "--json"}
	if recast {
		argv = append(argv, "--recast")
	}
	return argv
}

// CatchStopArgv drops the watcher when auto-catch is switched off.
func CatchStopArgv() []string { return []string{"catch-stop", "--json"} }

// SellTickArgv is one auto-sell round.
func SellTickArgv() []string { return []string{"sell-tick", "--json"} }

// SellListArgv reads the whitelist, optionally toggling one item type on the
// way. A nil add or remove leaves the list alone.
func SellListArgv(add, remove *int) []string {
	argv := []string{"sell-list", "--json"}
	if add != nil {
		argv = append(argv, "--add", strconv.Itoa(*add))
	}
	if remove != nil {
		argv = append(argv, "--remove", strconv.Itoa(*remove))
	}
	return argv
}

// FreezeArgv holds values against the game. Unlike the rounds above this is a
// blocking loop, so the window runs it as its own process rather than ticking
// it, and stops it by killing that process.
func FreezeArgv(godmode, mana bool) []string {
	argv := []string{"freeze"}
	if godmode {
		argv = append(argv, "--godmode")
	}
	if mana {
		argv = append(argv, "--mana")
	}
	return argv
}

/*
Replies is every JSON object in a reply, in order.

The worker answers with a line of JSON, sometimes preceded by human-readable
output, so a reply is read by walking the lines and keeping the ones that decode.
Only lines starting with "{" are considered, so anything that decodes is an
object: an array or a bare string on its own line is skipped before parsing
rather than filtered after.
*/
func Replies(raw string) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			continue
		}
		out = append(out, got)
	}
	return out
}

// ParseSellList is the whitelist as item types, and whether the reply carried
// one at all.
func ParseSellList(raw string) ([]int, bool) {
	for _, got := range Replies(raw) {
		raw, ok := got["whitelist"]
		if !ok {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			continue
		}
		out := make([]int, 0, len(list))
		for _, v := range list {
			if n, ok := v.(float64); ok {
				out = append(out, int(n))
			}
		}
		return out, true
	}
	return nil, false
}

/*
Sample is one operation this package can emit, with the subcommand it is
declared to reach.

Samples() must list every builder. The parity test parses each argv with the
real CLI parser, so a builder that emits an argv the CLI will not accept fails
there instead of in the game -- which is how `long-reach 20` was caught, having
been written without the --tiles the parser requires.

The expected subcommand is written beside the sample rather than derived from
argv[0], because deriving it only proves argv[0] equals itself: a builder that
switched to a different valid subcommand would pass.
*/
type Sample struct {
	Name string   // the builder, for the failure message
	Cmd  string   // the subcommand it is declared to reach
	Argv []string // a representative argv
}

// Samples is every operation this package builds. A new builder goes here, or
// nothing checks it.
func Samples() []Sample {
	return []Sample{
		{"StatusArgv", "status", StatusArgv()},
		{"InventoryArgv", "inventory", InventoryArgv()},
		{"SetHPArgv", "set-hp", SetHPArgv("max")},
		{"SetManaArgv", "set-mana", SetManaArgv("max")},
		{"SetMaxHPArgv", "set-max-hp", SetMaxHPArgv(400)},
		{"SetMaxManaArgv", "set-max-mana", SetMaxManaArgv(200)},
		{"FastMiningArgv", "fast-mining", FastMiningArgv()},
		{"LongReachArgv", "long-reach", LongReachArgv(20)},
		{"PotionsArgv", "potions", PotionsArgv(1)},
		{"FishingArgv", "fishing", FishingArgv(30, true)},
		{"FishingArgv/no-kit", "fishing", FishingArgv(30, false)},
		{"FishingPowerArgv", "fishing", FishingPowerArgv(255)},
		{"FishingRestoreArgv", "fishing", FishingRestoreArgv()},
		{"FishingBuffsArgv", "fishing-buffs", FishingBuffsArgv(true, true, true)},
		{"FishingBuffsArgv/none", "fishing-buffs", FishingBuffsArgv(false, false, false)},
		{"CatchArgv", "catch-tick", CatchArgv(false)},
		{"CatchArgv/recast", "catch-tick", CatchArgv(true)},
		{"CatchStopArgv", "catch-stop", CatchStopArgv()},
		{"SellTickArgv", "sell-tick", SellTickArgv()},
		{"SellListArgv", "sell-list", SellListArgv(nil, nil)},
		{"SellListArgv/add", "sell-list", SellListArgv(ptr(29), nil)},
		{"SellListArgv/remove", "sell-list", SellListArgv(nil, ptr(29))},
		{"FreezeArgv", "freeze", FreezeArgv(true, true)},
		{"FreezeArgv/none", "freeze", FreezeArgv(false, false)},
	}
}

// ptr is a sample helper: the optional arguments above are pointers so that
// "not given" is distinct from zero.
func ptr(n int) *int { return &n }

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
