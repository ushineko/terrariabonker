package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/projectile"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Auto-catch and the projectile editor: the two things that act on what is in
flight rather than on what is in a slot.
*/

// catchLine is one auto-catch event as the command line reports it.
func catchLine(e service.CatchEvent) string {
	if e.What == "cast" {
		if e.Confirmed != nil && *e.Confirmed {
			return "[catch] cast the line"
		}
		return "[catch] tried to cast and no line went out"
	}
	what := fmt.Sprintf("NPC %d", -e.Catch)
	if e.Catch > 0 {
		if names, err := game.ItemNames(); err == nil {
			what = names.Name(int(e.Catch))
		}
	}
	return "[catch] reeled in " + what
}

func (a *App) catchCmd() *cobra.Command {
	var recast, asJSON, force bool
	var rounds int
	cmd := &cobra.Command{
		Use:   "catch",
		Short: "reel in every bite for you (needs the auto-use cheat on)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			hint := " (pass --recast to cast for you too)"
			if recast {
				hint = ""
			}
			printf(cmd, "[catch] watching for bites. Ctrl-C to stop.%s", hint)
			run, err := got.Svc.WatchCatch(ctxOf(cmd), got.Patcher, recast,
				service.CastConfirm, 0, rounds, func(e service.CatchEvent) {
					printf(cmd, "%s", catchLine(e))
				})
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, run)
			}
			line := fmt.Sprintf("[catch] %d fish over %d rounds", run.Caught, run.Rounds)
			if run.Cast > 0 {
				line += fmt.Sprintf(", %d casts", run.Cast)
			}
			if run.CastMissed > 0 {
				line += fmt.Sprintf(", %d that did not go out", run.CastMissed)
			}
			printf(cmd, "%s", line)
			return nil
		},
	}
	cmd.Flags().BoolVar(&recast, "recast", false,
		"cast again when the water is empty, once you have cast once")
	cmd.Flags().IntVar(&rounds, "rounds", 0, "stop after this many rounds")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) catchTickCmd() *cobra.Command {
	var recast, asJSON, force bool
	var budget float64
	cmd := &cobra.Command{
		Use:   "catch-tick",
		Short: "one slice of auto-catch (GUI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			tick, err := got.Svc.CatchTick(got.Patcher, recast, seconds(budget))
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			events, _ := tick["events"].([]service.CatchEvent)
			for _, e := range events {
				printf(cmd, "%s", catchLine(e))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&recast, "recast", false, "cast when the water is empty")
	cmd.Flags().Float64Var(&budget, "budget", 0.30,
		"seconds to spend watching in this call")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) catchStopCmd() *cobra.Command {
	var asJSON, force bool
	cmd := &cobra.Command{
		Use:   "catch-stop",
		Short: "drop the auto-catch watcher",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			stopped := got.Svc.CatchStop(got.Patcher)
			if asJSON {
				return printJSON(cmd, stopped)
			}
			printf(cmd, "[catch] stopped")
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

/*
parseOverrides turns `837:tileCollide=0` into what the editor takes.

An unknown field name is refused here rather than dropped: a typo in a saved
profile would otherwise look exactly like a field that does nothing, which is a
diagnosis this project has already paid for once.
*/
func parseOverrides(pairs []string) (map[int32]map[string]float64, error) {
	out := map[int32]map[string]float64{}
	for _, raw := range pairs {
		head, value, hasValue := strings.Cut(raw, "=")
		ptype, name, hasField := strings.Cut(head, ":")
		if !hasValue || !hasField || value == "" || name == "" {
			return nil, usagef("bad --set %q; expected TYPE:FIELD=VALUE", raw)
		}
		field, known := projectile.Fields[name]
		if !known {
			return nil, usagef("unknown field %q; known: %s", name, knownFields())
		}
		num, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, usagef("bad value for %s: %q", name, value)
		}
		if field.Kind != "f32" {
			num = float64(int64(num))
		}
		id, err := strconv.Atoi(ptype)
		if err != nil {
			return nil, usagef("bad --set %q; expected TYPE:FIELD=VALUE", raw)
		}
		key := int32(id) //nolint:gosec // a projectile type the user typed
		if out[key] == nil {
			out[key] = map[string]float64{}
		}
		out[key][name] = num
	}
	return out, nil
}

// knownFields is the editable field names, in order, for the failure message.
func knownFields() string {
	names := make([]string, 0, len(projectile.Fields))
	for name := range projectile.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (a *App) projectileTickCmd() *cobra.Command {
	var asJSON, force bool
	var set []string
	var budget float64
	cmd := &cobra.Command{
		Use:   "projectile-tick",
		Short: "one slice of projectile editing (GUI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			overrides, err := parseOverrides(set)
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			tick, err := got.Svc.ProjectileTick(overrides, seconds(budget))
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			patched, _ := tick["patched"].(int)
			sweeps, _ := tick["sweeps"].(int)
			types, _ := tick["types"].(map[int32]int)
			printf(cmd, "[projectile] %d field writes over %d sweeps%s",
				patched, sweeps, inFlight(types))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&set, "set", nil,
		"e.g. 837:tileCollide=0 (repeatable)")
	cmd.Flags().Float64Var(&budget, "budget", 0.25,
		"seconds to spend enforcing in this call")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

// inFlight is what a sweep found, or that it found nothing.
func inFlight(types map[int32]int) string {
	if len(types) == 0 {
		return " (nothing in flight)"
	}
	ids := make([]int32, 0, len(types))
	for id := range types {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%dx%d", id, types[id]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (a *App) projectileStopCmd() *cobra.Command {
	var asJSON, force bool
	cmd := &cobra.Command{
		Use:   "projectile-stop",
		Short: "drop the projectile editor state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			stopped := got.Svc.ProjectileStop()
			if asJSON {
				return printJSON(cmd, stopped)
			}
			printf(cmd, "[projectile] stopped")
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) projectileOfCmd() *cobra.Command {
	var asJSON, force bool
	cmd := &cobra.Command{
		Use:   "projectile-of ITEM",
		Short: "which projectile an item fires",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			itemType, err := atoi32(args[0])
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			shot, err := got.Svc.ProjectileOf(itemType)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, shot)
			}
			item, _ := shot["item"].(int32)
			shoot, _ := shot["shoot"].(int32)
			if shoot == 0 {
				printf(cmd, "item %d fires no projectile", item)
				return nil
			}
			printf(cmd, "item %d shoots projectile %d", item, shoot)
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}
