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
	"sort"
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

/*
BuildKey is the build the gate and the patch report key on.

Version plus Steam buildid, because that pair is what a byte pattern is really
pinned to: a rebuild can carry the same version string and different code. Falls
back to the version alone when the buildid could not be read.
*/
func (s *Status) BuildKey() string {
	if s == nil {
		return ""
	}
	if s.BuildID != "" && s.Build != "" {
		return s.Build
	}
	return s.Version
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

// --- the inventory ------------------------------------------------------------

// ItemSlot is one slot of the player's inventory, as `inventory --all --json`
// reports it. An empty slot has Type 0 and is still a slot the grid draws.
type ItemSlot struct {
	Slot      int            `json:"slot"`
	Type      int            `json:"type"`
	Stack     int            `json:"stack"`
	Damage    int            `json:"damage"`
	AutoReuse int            `json:"auto_reuse"`
	UseTime   int            `json:"use_time"`
	Pick      int            `json:"pick"`
	TileBoost int            `json:"tile_boost"`
	UseAnim   int            `json:"use_anim"`
	Rare      int            `json:"rare"`
	Defense   int            `json:"defense"`
	Prefix    int            `json:"prefix"`
	Flags     map[string]any `json:"flags"`
}

// Empty reports whether a slot holds nothing. Type 0 is the game's own way of
// saying so.
func (s ItemSlot) Empty() bool { return s.Type == 0 }

// ParseSlots decodes an inventory reply.
func ParseSlots(raw string) ([]ItemSlot, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var out []ItemSlot
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		return nil, false
	}
	return out, true
}

// SetStackArgv changes how many of a thing a slot holds.
func SetStackArgv(slot, value int) []string {
	return []string{"set-stack", strconv.Itoa(slot), strconv.Itoa(value)}
}

/*
ItemEdit is what set-item may change about a slot. A nil field is left alone,
which is why they are pointers: zero is a value the game accepts for most of
these, so "unset" cannot be spelled as 0.

ExpectType guards the write. The grid re-reads once a second, so an edit is
built on a snapshot that may already be stale; naming the type the editor was
opened on makes the CLI refuse a write aimed at whatever has since replaced it.
*/
type ItemEdit struct {
	Stack      *int
	Damage     *int
	AutoReuse  *int
	UseTime    *int
	UseAnim    *int
	Pick       *int
	TileBoost  *int
	Defense    *int
	Prefix     *int
	ExpectType *int
}

// SetItemArgv places or edits an item in a slot.
func SetItemArgv(slot, itemType int, e ItemEdit) []string {
	argv := []string{"set-item", strconv.Itoa(slot), strconv.Itoa(itemType)}
	// --expect-type first, as the Python builder emits it, so the two argvs are
	// comparable line for line while both exist.
	for _, f := range []struct {
		flag string
		v    *int
	}{
		{"--expect-type", e.ExpectType},
		{"--stack", e.Stack},
		{"--damage", e.Damage},
		{"--auto-reuse", e.AutoReuse},
		{"--use-time", e.UseTime},
		{"--use-anim", e.UseAnim},
		{"--pick", e.Pick},
		{"--tile-boost", e.TileBoost},
		{"--defense", e.Defense},
		{"--prefix", e.Prefix},
	} {
		if f.v != nil {
			argv = append(argv, f.flag, strconv.Itoa(*f.v))
		}
	}
	return argv
}

// GiveArgv puts an item into the first free slot.
func GiveArgv(itemType, stack int) []string {
	return []string{"give", strconv.Itoa(itemType), "--stack", strconv.Itoa(stack)}
}

// --- static catalogs -----------------------------------------------------------

// Item is one entry of the item catalog. Stats come from the game's own
// template objects, so they are empty for a build that has not been scanned.
type Item struct {
	ID      int                `json:"id"`
	Name    string             `json:"name"`
	Kind    string             `json:"kind"`
	Tooltip string             `json:"tooltip"`
	Wiki    string             `json:"wiki"`
	Stats   map[string]float64 `json:"stats"`
}

// NPC is one entry of the NPC catalog.
type NPC struct {
	ID    int                `json:"id"`
	Name  string             `json:"name"`
	Kind  string             `json:"kind"`
	Wiki  string             `json:"wiki"`
	NetID int                `json:"net_id"`
	Stats map[string]float64 `json:"stats"`
}

// Stat reads one of an entry's stats, and says whether it was reported at all:
// a damage of 0 and a damage the game never told us apart.
func Stat(stats map[string]float64, key string) (float64, bool) {
	v, ok := stats[key]
	return v, ok
}

// Compendium is the full catalog: every item, and every NPC.
type Compendium struct {
	Items []Item `json:"items"`
	NPCs  []NPC  `json:"npcs"`
}

// CompendiumArgv reads the catalog. It needs the game: item stats come from the
// game's own template objects, and the result is cached per build.
func CompendiumArgv(refresh bool) []string {
	argv := []string{"compendium", "--json"}
	if refresh {
		argv = append(argv, "--refresh")
	}
	return argv
}

// ParseCompendium decodes the catalog.
func ParseCompendium(raw string) (*Compendium, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var c Compendium
	if err := json.Unmarshal([]byte(line), &c); err != nil || c.Items == nil {
		return nil, false
	}
	return &c, true
}

// Prefix is one item modifier: what it is called and whether it helps.
type Prefix struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Quality string `json:"quality"` // "good", "bad" or "neutral"
}

/*
The projectile editor.

Nothing here is written into the game's data. Projectiles are transient objects
and SetDefaults rebuilds each one from the game's own literals, so the edit has
to be re-applied to whatever is in flight, several times a second, for as long as
the cheat is on. Switching it off restores nothing because there is nothing to
restore.
*/

// ProjectileKind is how wide a field is written, which decides what a value may
// be: a bool is a byte, and a size is a float.
const (
	KindBool  = "b8"
	KindInt   = "i32"
	KindFloat = "f32"
)

// ProjectileField is one editable field of a projectile in flight. The names
// and the limits are the CLI's own -- projectile_edit.FIELDS -- and a test
// asks the Python for them rather than trusting this list.
type ProjectileField struct {
	Name   string
	Label  string
	Kind   string
	Lo, Hi float64
}

// ProjectileFields is what the section offers, in the order the Qt panel had.
var ProjectileFields = []ProjectileField{
	{Name: "tileCollide", Label: "Pass through blocks", Kind: KindBool, Lo: 0, Hi: 1},
	{Name: "penetrate", Label: "Enemies pierced", Kind: KindInt, Lo: -1, Hi: 999},
	{Name: "extraUpdates", Label: "Extra ticks per frame", Kind: KindInt, Lo: 0, Hi: 16},
	{Name: "scale", Label: "Size", Kind: KindFloat, Lo: 0.05, Hi: 10},
	{Name: "timeLeft", Label: "Lifetime (ticks)", Kind: KindInt, Lo: 1, Hi: 216000},
}

// ProjectileOfArgv asks which projectile an item fires (Item.shoot).
func ProjectileOfArgv(itemType int) []string {
	return []string{"projectile-of", strconv.Itoa(itemType), "--json"}
}

/*
ProjectileTickArgv is one slice of enforcement.

overrides is {projectile type: {field: value}}. Emitted sorted, so the same
overrides always produce the same argv: a command line that reorders itself
between calls is miserable to compare in a log or in a test.
*/
func ProjectileTickArgv(overrides map[int]map[string]float64) []string {
	argv := []string{"projectile-tick", "--json",
		"--budget", strconv.FormatFloat(projectileBudget, 'g', -1, 64)}
	types := make([]int, 0, len(overrides))
	for t := range overrides {
		types = append(types, t)
	}
	sort.Ints(types)
	for _, t := range types {
		fields := make([]string, 0, len(overrides[t]))
		for name := range overrides[t] {
			fields = append(fields, name)
		}
		sort.Strings(fields)
		for _, name := range fields {
			argv = append(argv, "--set", strconv.Itoa(t)+":"+name+"="+
				strconv.FormatFloat(overrides[t][name], 'g', -1, 64))
		}
	}
	return argv
}

/*
projectileBudget is how long one slice may spend enforcing, in seconds.

The worker answers one request at a time, so this is also how long everything
else waits behind it. A quarter of a second is the Qt panel's own figure: long
enough to sweep the slots that are in flight, short enough that the inventory
sync does not visibly stutter behind it.
*/
const projectileBudget = 0.25

// ProjectileStopArgv tells the worker to forget its per-projectile state. Sent
// once when the cheat goes off: a slot it still remembers would keep it from
// re-applying a once-per-projectile field to a slot the game has reused.
func ProjectileStopArgv() []string { return []string{"projectile-stop", "--json"} }

/*
The build gate (spec 036).

The patches are matched by byte pattern against one exact game build, so an
update has three outcomes -- everything still matches, some of it does, or none
of it -- and the window has to be able to tell which before it writes anything.
BuildCheck patches nothing; it reports.
*/
const (
	// DecisionAccepted records a build where every cheat still resolved.
	DecisionAccepted = "accepted"
	// DecisionDegraded records a build the user chose to run with some cheats
	// switched off, and names them so they stay off.
	DecisionDegraded = "degraded"
)

// CheatProbe is one cheat's verdict on the running build.
type CheatProbe struct {
	Resolved bool   `json:"resolved"`
	Sites    int    `json:"sites"`
	Reason   string `json:"reason"`
}

// BuildCheck is what the game is, and whether the cheats still fit it.
type BuildCheck struct {
	Build   string `json:"build"`
	Level   string `json:"level"`
	Runtime string `json:"runtime"`
	// Message is the CLI's own sentence about how this build compares with the
	// one the patterns were derived on. Shown as it stands rather than restated
	// here, so the known-good build is spelled in one place.
	Message string `json:"message"`
	// Known is whether the project verified this build; Recognised is that, or
	// this machine having already decided about it.
	Known      bool   `json:"known"`
	Recognised bool   `json:"recognised"`
	Decision   string `json:"decision"`
	// Failed is what did not resolve in this probe. DecidedFailed is what was
	// recorded as dead when the decision was made, which is the list to honour:
	// a cheat the user chose to run without stays off even if it resolves again.
	Failed        []string              `json:"failed"`
	DecidedFailed []string              `json:"decided_failed"`
	Cheats        map[string]CheatProbe `json:"cheats"`
}

// BuildCheckArgv asks what build is running and whether the cheats resolve on
// it. It needs the game, and it writes nothing.
func BuildCheckArgv() []string { return []string{"build-check", "--json"} }

// ParseBuildCheck decodes a build report.
func ParseBuildCheck(raw string) (*BuildCheck, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var b BuildCheck
	if err := json.Unmarshal([]byte(line), &b); err != nil || b.Build == "" {
		return nil, false
	}
	return &b, true
}

// AcceptBuildArgv records this machine's decision about the running build, so
// the gate does not ask again. failed is sorted, because the CLI stores it and
// two orderings of one answer would read as two answers.
func AcceptBuildArgv(decision string, failed []string) []string {
	argv := []string{"accept-build", decision, "--json"}
	if len(failed) > 0 {
		sorted := append([]string(nil), failed...)
		sort.Strings(sorted)
		argv = append(argv, "--failed")
		argv = append(argv, sorted...)
	}
	return argv
}

// SpawnNPCArgv puts an NPC in the world, a distance away in tiles.
func SpawnNPCArgv(netID, distance int) []string {
	return []string{"spawn-npc", strconv.Itoa(netID), "--distance", strconv.Itoa(distance)}
}

// ExtractSpritesArgv builds the item icon cache from the game's own files. It
// runs unprivileged so the cache belongs to the user rather than to root.
func ExtractSpritesArgv(force bool) []string {
	argv := []string{"extract-sprites"}
	if force {
		argv = append(argv, "--force")
	}
	return argv
}

// ExtractRecipesArgv reads the crafting recipes out of the game.
func ExtractRecipesArgv() []string { return []string{"extract-recipes"} }

/*
Recipe is one crafting recipe: what it makes, how many, and what it takes.

The field names are the cache's own, short because there are thousands of them:
"out" is the item made, "n" how many, "ing" the ingredients as [item, count]
pairs, "tile" the crafting station when one is needed.
*/
type Recipe struct {
	Out  int     `json:"out"`
	N    int     `json:"n"`
	Ing  [][]int `json:"ing"`
	Tile *int    `json:"tile"`
}

// Recipes is the cached recipe book: the recipes themselves, and the names of
// the crafting stations they call for.
type Recipes struct {
	Recipes  []Recipe          `json:"recipes"`
	Stations map[string]string `json:"stations"`
}

// RecipesArgv reads the cached recipe book. Static and unprivileged: browsing
// it is offline work, and `extract-recipes` is what fills it.
func RecipesArgv() []string { return []string{"recipes", "--json"} }

// ParseRecipes decodes the recipe book.
func ParseRecipes(raw string) (*Recipes, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var r Recipes
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return nil, false
	}
	return &r, true
}

// Station names the crafting station a recipe needs, or says it is made by
// hand when it needs none.
func (r Recipe) Station(stations map[string]string) string {
	if r.Tile == nil {
		return "by hand"
	}
	if n, ok := stations[strconv.Itoa(*r.Tile)]; ok && n != "" {
		return n
	}
	return "tile " + strconv.Itoa(*r.Tile)
}

// PrefixesArgv reads the modifier catalog. Static, so it is read through a
// one-shot run rather than the worker, which needs a game.
func PrefixesArgv() []string { return []string{"prefixes", "--json"} }

// ParsePrefixes decodes the modifier catalog.
func ParsePrefixes(raw string) ([]Prefix, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var out []Prefix
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		return nil, false
	}
	return out, true
}

// --- code patches ------------------------------------------------------------

// Patch is one entry of the patch catalog: what it is called, what it does, and
// the number it takes when it takes one.
type Patch struct {
	Name    string     `json:"name"`
	Label   string     `json:"label"`
	Note    string     `json:"note"`
	Section string     `json:"section"`
	Kind    string     `json:"kind"`
	Value   *ValueSpec `json:"value"`
}

// ValueSpec is the number a patch takes: its kind, its range, and either a unit
// or a set of presets to choose from.
type ValueSpec struct {
	Kind    string   `json:"kind"` // "i32" or "f32"
	Default float64  `json:"default"`
	Lo      float64  `json:"lo"`
	Hi      float64  `json:"hi"`
	Unit    string   `json:"unit"`
	Presets []Preset `json:"presets"`
}

// Preset is one named value for a patch that offers a choice rather than a
// range.
type Preset struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

/*
PatchCatalogArgv reads the catalog: every patch, its label, its note and its
range.

Static data, and the one thing the Qt panel got by importing the Python
in-process rather than through this contract. It must be read through a one-shot
CLI run and not the warm worker: the worker connects to a Service before it
dispatches, so through it this would need Terraria running, and the controls are
built before anything is attached.
*/
func PatchCatalogArgv() []string { return []string{"patch", "catalog", "--json"} }

// ParsePatchCatalog decodes the catalog.
func ParsePatchCatalog(raw string) ([]Patch, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var out []Patch
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		return nil, false
	}
	return out, true
}

// PatchStatus is which patches are on and what they are set to.
type PatchStatus struct {
	On     map[string]bool    `json:"on"`
	Values map[string]float64 `json:"values"`
	Build  string             `json:"build"`
	// BuildVerified is whether every AOB matched on this game build. False
	// means at least one patch is aimed at a signature that has moved.
	BuildVerified bool `json:"build_verified"`
	// Detail is the per-patch verdict, which is what a control needs: the
	// summary above cannot say which of twelve patches is the one that moved.
	Detail map[string]PatchDetail `json:"detail"`
}

/*
PatchDetail is one patch's standing on the running build.

Available and Verified are different failures and the interface has to tell them
apart: unavailable means the pattern does not resolve here, so the control must
not be clickable; unverified means it resolves but was confirmed on another
build, so it works and the user should know it is unproven.
*/
type PatchDetail struct {
	On        bool   `json:"on"`
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Reason    string `json:"reason"`
}

// PatchStatusArgv asks what is currently patched into the running game.
func PatchStatusArgv() []string { return []string{"patch", "status", "--json"} }

// ParsePatchStatus decodes a status reply.
func ParsePatchStatus(raw string) (*PatchStatus, bool) {
	line, ok := lastLine(raw)
	if !ok {
		return nil, false
	}
	var st PatchStatus
	if err := json.Unmarshal([]byte(line), &st); err != nil {
		return nil, false
	}
	if st.On == nil {
		return nil, false
	}
	return &st, true
}

// PatchSetArgv turns one patch on or off. A nil value takes the patch's own
// default.
func PatchSetArgv(name string, on bool, value *float64) []string {
	if !on {
		return []string{"patch", "disable", name}
	}
	argv := []string{"patch", "enable", name}
	if value != nil {
		argv = append(argv, "--value", strconv.FormatFloat(*value, 'g', -1, 64))
	}
	return argv
}

// RestoreArgv puts back what the saved profile says should be on, after a game
// restart or a world change has cleared it.
func RestoreArgv() []string { return []string{"restore", "--json"} }

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
		{"PatchCatalogArgv", "patch", PatchCatalogArgv()},
		{"PatchStatusArgv", "patch", PatchStatusArgv()},
		{"PatchSetArgv/on", "patch", PatchSetArgv("mining", true, nil)},
		{"PatchSetArgv/value", "patch", PatchSetArgv("mining", true, fptr(0.2))},
		{"PatchSetArgv/off", "patch", PatchSetArgv("mining", false, nil)},
		{"RestoreArgv", "restore", RestoreArgv()},
		{"SetStackArgv", "set-stack", SetStackArgv(3, 99)},
		{"SetItemArgv", "set-item", SetItemArgv(3, 29, ItemEdit{})},
		{"SetItemArgv/full", "set-item", SetItemArgv(3, 29, ItemEdit{
			Stack: ptr(1), Damage: ptr(50), AutoReuse: ptr(1), UseTime: ptr(10),
			UseAnim: ptr(10), Pick: ptr(100), TileBoost: ptr(5), Defense: ptr(2),
			Prefix: ptr(27), ExpectType: ptr(29),
		})},
		{"GiveArgv", "give", GiveArgv(29, 1)},
		{"CompendiumArgv", "compendium", CompendiumArgv(false)},
		{"CompendiumArgv/refresh", "compendium", CompendiumArgv(true)},
		{"PrefixesArgv", "prefixes", PrefixesArgv()},
		{"SpawnNPCArgv", "spawn-npc", SpawnNPCArgv(50, 10)},
		{"ExtractSpritesArgv", "extract-sprites", ExtractSpritesArgv(false)},
		{"ExtractSpritesArgv/force", "extract-sprites", ExtractSpritesArgv(true)},
		{"ExtractRecipesArgv", "extract-recipes", ExtractRecipesArgv()},
		{"RecipesArgv", "recipes", RecipesArgv()},
		{"ProjectileOfArgv", "projectile-of", ProjectileOfArgv(3507)},
		{"ProjectileTickArgv", "projectile-tick", ProjectileTickArgv(nil)},
		{"ProjectileTickArgv/set", "projectile-tick", ProjectileTickArgv(
			map[int]map[string]float64{
				837: {"tileCollide": 0, "scale": 2.5},
				14:  {"penetrate": -1},
			})},
		{"ProjectileStopArgv", "projectile-stop", ProjectileStopArgv()},
		{"BuildCheckArgv", "build-check", BuildCheckArgv()},
		{"AcceptBuildArgv", "accept-build", AcceptBuildArgv(DecisionAccepted, nil)},
		{"AcceptBuildArgv/degraded", "accept-build",
			AcceptBuildArgv(DecisionDegraded, []string{"reach", "mining"})},
	}
}

// ptr is a sample helper: the optional arguments above are pointers so that
// "not given" is distinct from zero.
func ptr(n int) *int { return &n }

func fptr(f float64) *float64 { return &f }

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
