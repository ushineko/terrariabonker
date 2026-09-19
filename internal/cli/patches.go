package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/builds"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/trainer"
)

/*
The code-patch cheats, the build gate around them, and the freeze loop that
holds a value against the game.
*/

/*
patchActions are what the first argument may be, and patchArgs is the rule.

`on` and `off` are the short spellings, kept because they are what the shell
history of everybody who has used this holds.
*/
var patchActions = []string{"status", "catalog", "enable", "disable", "on", "off"}

func patchArgs(_ *cobra.Command, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usagef("patch takes an action and, for enable and disable, a cheat")
	}
	if !oneOf(args[0], patchActions) {
		return usagef("%q is not one of %s", args[0], strings.Join(patchActions, ", "))
	}
	if len(args) == 2 && patch.Injections[args[1]].Anchor == "" &&
		patch.Cheats[args[1]].Anchor == "" {
		return usagef("%q is not a patch", args[1])
	}
	return nil
}

// oneOf reports whether a word is in a list.
func oneOf(want string, list []string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func (a *App) patchCmd() *cobra.Command {
	var force, asJSON bool
	var value float64
	cmd := &cobra.Command{
		Use:   "patch ACTION [CHEAT]",
		Short: "code-patch cheats (mining/reach/placement)",
		/*
			Each position is checked against its own list.

			cobra's own OnlyValidArgs checks every argument against one list,
			which here would mean a cheat name had to also be an action -- so
			`patch enable mining` was refused, which is the commonest thing
			anybody types.
		*/
		Args: patchArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			action := args[0]
			/*
				The catalog is static -- labels, notes, sections and value ranges
				-- and is answered before attaching, so it works with the game
				closed. The window reads it while building its controls, which
				happens before anything is attached.
			*/
			if action == "catalog" {
				return printJSON(cmd, patchCatalog())
			}
			var cheat string
			if len(args) > 1 {
				cheat = args[1]
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			switch action {
			case "status":
				return a.patchStatus(cmd, got, asJSON)
			case "enable", "on":
				if cheat == "" {
					return usagef("%s needs a cheat to turn on", action)
				}
				var v *float64
				if cmd.Flags().Changed("value") {
					v = &value
				}
				if err := got.Patcher.Enable(cheat, v); err != nil {
					return err
				}
				if v != nil {
					printf(cmd, "[OK] enabled %s (value %g)", cheat, *v)
				} else {
					printf(cmd, "[OK] enabled %s", cheat)
				}
				return nil
			case "disable", "off":
				if cheat == "" {
					return usagef("%s needs a cheat to turn off", action)
				}
				if err := got.Patcher.Disable(cheat); err != nil {
					return err
				}
				printf(cmd, "[OK] disabled %s", cheat)
				return nil
			}
			return usagef("%q is not an action", action)
		},
	}
	cmd.Flags().Float64Var(&value, "value", 0,
		"override the enabled value (mining pickSpeed, reach tiles)")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

/*
patchStatus is what is applied right now, and what could be.

The window polls this every couple of seconds, which is the one moment the game
can be counted on to be focused and running frames. The arena is allocated then,
so toggling a cheat later -- which means clicking away from the game and pausing
it -- does not have to.
*/
func (a *App) patchStatus(cmd *cobra.Command, got *Game, asJSON bool) error {
	got.Svc.EnsureArena(got.Patcher)
	build := got.Svc.BuildKey()
	detail := got.Patcher.Details(build)
	values := got.Patcher.Values()

	on := make(map[string]bool, len(detail))
	verifiedEverywhere := true
	for name, d := range detail {
		on[name] = d.On
		if !d.Verified {
			verifiedEverywhere = false
		}
	}
	if asJSON {
		return printJSON(cmd, map[string]any{
			"on": on, "values": values, "detail": detail,
			"build": build, "build_verified": verifiedEverywhere,
		})
	}
	printf(cmd, "  build %s", build)
	for _, info := range patch.Catalog() {
		d := detail[info.Name]
		mark := " "
		if d.On {
			mark = "x"
		}
		shown := ""
		if v, tuned := values[info.Name]; tuned {
			shown = fmt.Sprintf("  = %g", v)
		}
		note := ""
		switch {
		case !d.Available:
			note = "   UNAVAILABLE: " + d.Reason
		case !d.Verified:
			note = "   (AOB unverified on this build)"
		}
		printf(cmd, "  [%s] %-11s %s%s%s", mark, info.Name, info.Label, shown, note)
	}
	return nil
}

/*
patchCatalog is the static description of every cheat.

It exists because the catalog was never part of the command line's contract: the
window used to import it in-process, which a front end in another language
cannot do, and copying it would be a second spelling of every label and range to
keep in step.
*/
func patchCatalog() []map[string]any {
	out := make([]map[string]any, 0, len(patch.Catalog()))
	for _, info := range patch.Catalog() {
		entry := map[string]any{
			"name": info.Name, "label": info.Label, "note": info.Note,
			"section": info.Section, "kind": info.Kind, "value": nil,
		}
		if v := info.Value; v != nil {
			presets := make([]map[string]any, 0, len(v.Presets))
			for _, p := range v.Presets {
				presets = append(presets, map[string]any{"label": p.Label, "value": p.Value})
			}
			// The kind is how the value is written into the game, which the
			// window needs in order to offer whole numbers where the game takes
			// whole numbers.
			kind := "i32"
			if v.F32 {
				kind = "f32"
			}
			entry["value"] = map[string]any{
				"kind": kind, "default": v.Default, "lo": v.Lo, "hi": v.Hi,
				"unit": v.Unit, "presets": presets,
			}
		}
		out = append(out, entry)
	}
	return out
}

func (a *App) restoreCmd() *cobra.Command {
	var force, asJSON bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "re-apply the saved profile (cheats + item edits) to the game",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			report, err := got.Svc.Restore(got.Patcher)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, report)
			}
			printf(cmd, "[OK] restored cheats=%v items=%v pending=%v skipped=%v",
				report.Cheats, report.Items, report.Pending, report.Skipped)
			return nil
		},
	}
	forceFlag(cmd, &force)
	jsonFlag(cmd, &asJSON)
	return cmd
}

func (a *App) buildCheckCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "build-check",
		Short: "report the running build and whether the cheats resolve on it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			report := got.Svc.BuildCheck(got.Patcher)
			if asJSON {
				return printJSON(cmd, report)
			}
			printf(cmd, "build %s (%s)", report.Build, report.Level)
			/*
				The runtime is reported because the patches match code its JIT
				emitted: a Proton update can break a cheat with the game
				untouched, and this is the only thing on screen that would show
				it changed.
			*/
			printf(cmd, "  runtime: %s", orUnknown(report.Runtime))
			printf(cmd, "  recognised: %t  known-good: %t  decision: %s",
				report.Recognised, report.Known, report.Decision)
			names := make([]string, 0, len(report.Cheats))
			for name := range report.Cheats {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				r := report.Cheats[name]
				mark := "NO "
				if r.Resolved {
					mark = "ok "
				}
				extra := ""
				if r.Reason != "" {
					extra = "  " + r.Reason
				}
				printf(cmd, "  %s %-16s sites=%d%s", mark, name, r.Sites, extra)
			}
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func (a *App) acceptBuildCmd() *cobra.Command {
	var asJSON bool
	var failed []string
	cmd := &cobra.Command{
		Use:   "accept-build DECISION",
		Short: "record this machine's decision about the running build",
		/*
			The decision, and then as many cheat names as --failed was given.

			The flag takes a list the way the parser this replaces did, where
			`--failed a b` is two names rather than one and a stray word. So the
			words after the first are folded into the flag -- and refused when
			the flag was not given at all, which is what makes a typo an error
			rather than a silently recorded cheat name.
		*/
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return usagef("accept-build needs a decision")
			}
			if !oneOf(args[0], []string{builds.Accepted, builds.Degraded}) {
				return usagef("%q is not %s or %s", args[0], builds.Accepted, builds.Degraded)
			}
			if len(args) > 1 && !cmd.Flags().Changed("failed") {
				return usagef("accept-build takes one decision")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			report, err := got.Svc.AcceptBuild(args[0], append(failed, args[1:]...))
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, report)
			}
			printf(cmd, "recorded %s as %s", report.Build, report.Decision)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&failed, "failed", nil,
		"cheats that did not resolve (recorded so they stay disabled)")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func (a *App) spawnNPCCmd() *cobra.Command {
	var force, asJSON bool
	var distance int
	cmd := &cobra.Command{
		Use:   "spawn-npc ID",
		Short: "spawn an NPC beside the player",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			netID, err := atoi32(args[0])
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			spawn, err := got.Svc.SpawnNPC(netID, distance)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, spawn)
			}
			printf(cmd, "spawned %s (#%d) in slot %d at tile (%.0f, %.0f), %d tiles from you",
				spawn.Name, spawn.ID, spawn.Slot, spawn.X, spawn.Y, spawn.TilesAway)
			return nil
		},
	}
	cmd.Flags().IntVar(&distance, "distance", 25,
		"tiles behind the player to place it (default 25)")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) freezeCmd() *cobra.Command {
	var force, godmode, mana bool
	var hz int
	var seconds float64
	cmd := &cobra.Command{
		Use:   "freeze",
		Short: "hold values against the game (godmode etc.)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.freeze(cmd, force, godmode, mana, hz, seconds)
		},
	}
	cmd.Flags().BoolVar(&godmode, "godmode", false, "pin HP to max")
	cmd.Flags().BoolVar(&mana, "mana", false, "pin mana to max")
	cmd.Flags().IntVar(&hz, "hz", trainer.DefaultHz, "rewrite frequency (default 200)")
	cmd.Flags().Float64Var(&seconds, "seconds", 0, "stop after N seconds")
	forceFlag(cmd, &force)
	return cmd
}

// godmodeCmd is shorthand for freeze --godmode, which is the one people type.
func (a *App) godmodeCmd() *cobra.Command {
	var force bool
	var hz int
	var seconds float64
	cmd := &cobra.Command{
		Use:   "godmode",
		Short: "shorthand for: freeze --godmode",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.freeze(cmd, force, true, false, hz, seconds)
		},
	}
	cmd.Flags().IntVar(&hz, "hz", trainer.DefaultHz, "rewrite frequency")
	cmd.Flags().Float64Var(&seconds, "seconds", 0, "stop after N seconds")
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) freeze(cmd *cobra.Command, force, godmode, mana bool,
	hz int, seconds float64) error {
	if !godmode && !mana {
		return usagef("pick at least one of --godmode / --mana")
	}
	got, err := a.game(true, force)
	if err != nil {
		return err
	}
	mem, ok := got.Svc.Mem.(trainer.Mem)
	if !ok {
		return fmt.Errorf("this game's memory cannot be frozen")
	}
	levers := ""
	for _, lever := range []struct {
		name string
		on   bool
	}{{"godmode", godmode}, {"infinite-mana", mana}} {
		if !lever.on {
			continue
		}
		if levers != "" {
			levers += ", "
		}
		levers += lever.name
	}
	f := trainer.New(mem, godmode, mana, hz)
	stop := "Ctrl-C to stop."
	if seconds > 0 {
		stop = fmt.Sprintf("Runs for %gs.", seconds)
	}
	announce := func(copies int) {
		printf(cmd, "[OK] freezing [%s] on %d copy(ies) at %d Hz. %s",
			levers, copies, hz, stop)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := f.Run(ctx, time.Duration(seconds*float64(time.Second)), announce); err != nil {
		return err
	}
	printf(cmd, "\n[done] %d restores applied.", f.Saves)
	return nil
}

// sellListing is the whitelist as both commands that show it print it.
func sellListing(names func(int) string, list []int32) string {
	if len(list) == 0 {
		return "empty"
	}
	out := ""
	for i, t := range list {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s (%d)", names(int(t)), t)
	}
	return out
}

// sellWhitelist is the saved list, in order.
func sellWhitelist() []int32 {
	list := profile.SellWhitelist()
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	return list
}
