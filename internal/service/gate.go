package service

import (
	"errors"
	"sort"
	"time"

	"github.com/ushineko/terrariabonker/internal/builds"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
What the window asks after a game update, and what it does with the answer.

Three separate things the panel needs and the memory layer cannot answer on its
own: whether the game is simulating at all, whether this build is one anybody
has decided about, and putting the player's saved configuration back into a
fresh game.
*/

/*
detectRuntime is the .NET runtime executing the game.

Named rather than called directly so a test can answer it: which runtime is
mapped is a fact about the machine the test is running on, and on a developer's
box the answer is always "none".
*/
var detectRuntime = version.DetectRuntime

// BuildReport is everything the panel needs to decide what to say about a build.
type BuildReport struct {
	Build   string        `json:"build"`
	Version string        `json:"version"`
	BuildID string        `json:"buildid"`
	Runtime string        `json:"runtime"`
	Level   version.Level `json:"level"`
	Message string        `json:"message"`
	// Known is whether this project verified an anchor against this build;
	// VerifiedEverywhere is whether it verified all of them.
	Known              bool `json:"known"`
	VerifiedEverywhere bool `json:"verified_everywhere"`
	// Decision is what this machine chose, if it has; Recognised is either that
	// or the project's own verification.
	Decision   string `json:"decision"`
	Recognised bool   `json:"recognised"`

	Cheats map[string]patch.CheatProbe `json:"cheats"`
	Failed []string                    `json:"failed"`
	/*
		DecidedFailed is what was recorded as dead when the decision was made,
		which is not the same list as this probe's: a cheat the user chose to run
		without stays off even if it resolves again, and a panel that only sees
		the fresh probe cannot honour that choice.
	*/
	DecidedFailed []string `json:"decided_failed"`
}

/*
BuildCheck answers "is this build one we know, and do the cheats still resolve
on it?". It patches nothing.
*/
func (s *Service) BuildCheck(p *patch.Patcher) BuildReport {
	key := s.BuildKey()
	info := s.BuildInfo()

	verified, everywhere := false, true
	for _, anchor := range patch.Anchors {
		if contains(anchor.Verified, key) {
			verified = true
		} else {
			everywhere = false
		}
	}

	decided, hasDecision := builds.Get(key)
	probe := p.Probe(key)
	failed := []string{}
	for name, r := range probe {
		if !r.Resolved {
			failed = append(failed, name)
		}
	}
	sort.Strings(failed)

	decidedFailed := []string{}
	if hasDecision {
		decidedFailed = append(decidedFailed, decided.Failed...)
		sort.Strings(decidedFailed)
	}

	return BuildReport{
		Build: key, Version: info.Version, BuildID: info.BuildID,
		Runtime: detectRuntime(s.PID),
		Level:   info.Level, Message: info.Message,
		Known: verified, VerifiedEverywhere: everywhere,
		Decision: decided.Decision, Recognised: verified || hasDecision,
		Cheats: probe, Failed: failed, DecidedFailed: decidedFailed,
	}
}

// contains reports whether a list has a string in it.
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Accepted is the decision that was recorded.
type Accepted struct {
	Build    string   `json:"build"`
	Decision string   `json:"decision"`
	Failed   []string `json:"failed"`
}

// AcceptBuild records this machine's decision about the running build.
func (s *Service) AcceptBuild(how string, failed []string) (Accepted, error) {
	key := s.BuildKey()
	if err := builds.Remember(key, how, failed, detectRuntime(s.PID)); err != nil {
		return Accepted{}, &Error{Message: err.Error()}
	}
	sorted := append([]string{}, failed...)
	sort.Strings(sorted)
	return Accepted{Build: key, Decision: how, Failed: sorted}, nil
}

/*
FramesAdvancing reports whether the game is simulating, or merely open.

Terraria pauses in single-player when its window loses focus, and a paused game
runs no frames. Sampled from Main's statics rather than from the player, because
a player standing still changes nothing -- which cost a wrong diagnosis once.
*/
func (s *Service) FramesAdvancing(window time.Duration) bool {
	base, ok := s.StaticBase()
	if !ok {
		return false
	}
	before := s.Mem.Read(base, 0x400)
	if len(before) == 0 {
		return false
	}
	time.Sleep(window)
	after := s.Mem.Read(base, 0x400)
	return len(after) == len(before) && string(after) != string(before)
}

// FrameWindow is how long FramesAdvancing watches for by default.
const FrameWindow = 80 * time.Millisecond

/*
EnsureArena allocates this program's memory now, while the game happens to be
running.

Every stub lives in the arena, and allocating it means asking the game to call
VirtualAlloc -- which needs frames. But enabling a cheat from the panel means
clicking the panel, which unfocuses the game, which pauses it. So the allocation
must not wait until the user asks for a cheat: it is done opportunistically
while the game is live, and by the time they toggle anything it is already
there.

Never fails loudly and never blocks on a paused game: if frames are not
advancing there is nothing to do yet, and the next poll tries again.
*/
func (s *Service) EnsureArena(p *patch.Patcher) bool {
	if p.ArenaReady() {
		return true
	}
	if !s.FramesAdvancing(FrameWindow) {
		return false
	}
	_, err := p.ArenaWithin(3 * time.Second)
	return err == nil
}

// ItemEditRecord is what was saved for an item type.
type ItemEditRecord struct {
	Type  int32              `json:"type"`
	Saved map[string]float64 `json:"saved"`
}

/*
RecordItemEdit saves the fields auto-restore needs: the ones the game
regenerates from the type.

Type, stack and prefix are written into the save by Terraria itself, so
recording them achieves nothing and produced restore warnings about items whose
only change was a prefix.

It deliberately does *not* drop fields that match the item's defaults. That was
tried and it destroyed real edits: the "default" came from the template scan,
which can pick up a live edited item as the template, so an edit was compared
against itself and pruned. Keeping a redundant field costs a harmless rewrite of
a value the game would have set anyway; dropping a real one loses the user's
work silently.
*/
func (s *Service) RecordItemEdit(itemType int32, fields map[string]float64) (ItemEditRecord, error) {
	want := map[string]float64{}
	for _, name := range profile.Restorable {
		if v, given := fields[name]; given {
			want[name] = v
		}
	}
	if err := profile.SetItemEdit(itemType, want); err != nil {
		return ItemEditRecord{}, &Error{Message: err.Error()}
	}
	return ItemEditRecord{Type: itemType, Saved: want}, nil
}

/*
RestoreReport is what a restore managed.

Pending is not a failure: a cheat whose method is not JIT-compiled yet cannot be
applied and can be retried in a moment. Absent is not one either -- an edit for
an item the player is no longer carrying is ordinary.
*/
type RestoreReport struct {
	Cheats  []string `json:"cheats"`
	Items   []int    `json:"items"`
	Pending []string `json:"pending"`
	Skipped []string `json:"skipped"`
	Absent  []int32  `json:"absent"`
}

/*
Restore re-applies the cross-session profile to this game.

Cheats first, each with its saved value. Then the item edits, matched by what
the item *is* rather than by the slot it sat in: an edited weapon the player
moved used to lose its edit silently. Every copy is re-edited, not just the
first.
*/
func (s *Service) Restore(p *patch.Patcher) (RestoreReport, error) {
	report := RestoreReport{
		Cheats: []string{}, Items: []int{}, Pending: []string{},
		Skipped: []string{}, Absent: []int32{},
	}
	for _, name := range patchOrder(profile.Cheats()) {
		value := profile.Cheats()[name]
		switch err := p.Enable(name, value); {
		case err == nil:
			report.Cheats = append(report.Cheats, name)
		case errors.Is(err, patch.ErrNotAPatch):
			report.Skipped = append(report.Skipped, "cheat:"+name)
		default:
			// The method is not JIT-ready yet, or the site is not what it should
			// be. Either way it is worth trying again rather than giving up.
			report.Pending = append(report.Pending, name)
		}
	}

	slots, err := s.Inventory()
	if err != nil {
		return report, err
	}
	edits := profile.ItemEdits()
	for _, itemType := range sortedTypes(edits) {
		where := []int{}
		for _, slot := range slots {
			if slot.Type == itemType {
				where = append(where, slot.Slot)
			}
		}
		if len(where) == 0 {
			report.Absent = append(report.Absent, itemType)
			continue
		}
		for _, slot := range where {
			if err := s.SetItem(slot, itemType, editFrom(edits[itemType])); err != nil {
				return report, err
			}
			report.Items = append(report.Items, slot)
		}
	}
	return report, nil
}

/*
editFrom turns a saved edit into the fields SetItem takes.

Only the restorable names are looked for, because only those are saved -- and a
field that is not in the table is not left out of the edit, it was never in the
profile to begin with.
*/
func editFrom(fields map[string]float64) ItemEdit {
	var edit ItemEdit
	i32 := func(name string) *int32 {
		v, given := fields[name]
		if !given {
			return nil
		}
		out := int32(v)
		return &out
	}
	edit.Damage = i32("damage")
	edit.UseTime = i32("use_time")
	edit.UseAnim = i32("use_anim")
	edit.Pick = i32("pick")
	edit.TileBoost = i32("tile_boost")
	edit.Defense = i32("defense")
	if v, given := fields["auto_reuse"]; given {
		on := v != 0
		edit.AutoReuse = &on
	}
	return edit
}

/*
patchOrder is the saved cheats in the order the catalog declares them.

A map would restore them in a different order every run, and the order is
visible: each one is applied to the running game, and the report names them in
the order they went in.
*/
func patchOrder(cheats map[string]*float64) []string {
	var out []string
	known := map[string]bool{}
	for _, info := range patch.Catalog() {
		known[info.Name] = true
		if _, wanted := cheats[info.Name]; wanted {
			out = append(out, info.Name)
		}
	}
	/*
		And then the names the catalog does not have, which are tried anyway.

		A profile written by a later version can name a cheat this one has never
		heard of. Skipping it silently here would hide it: it is reported as
		skipped instead, which is the truth and is what tells somebody their
		saved configuration is not all being applied.
	*/
	var unknown []string
	for name := range cheats {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return append(out, unknown...)
}

// sortedTypes is an edit table's item types in order, for the same reason.
func sortedTypes(edits map[int32]map[string]float64) []int32 {
	out := make([]int32, 0, len(edits))
	for t := range edits {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
