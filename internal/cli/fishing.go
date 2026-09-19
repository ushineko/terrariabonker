package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Fishing: the gear, the bait that does not run out, the potion effects without
the potions, and reeling every bite in.
*/

func (a *App) fishingCmd() *cobra.Command {
	var noKit, watch, restore, asJSON, force bool
	var keep, rounds, power int
	var interval float64
	cmd := &cobra.Command{
		Use:   "fishing",
		Short: "a rod and bait if you have none, and bait that lasts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			out := map[string]any{}

			if restore {
				done, err := got.Svc.RestoreFishingPower()
				if err != nil {
					return err
				}
				out["restore"] = done
				if !asJSON {
					printf(cmd, "[fishing] %s", restoredLine(done))
				}
				if asJSON {
					return printJSON(cmd, out)
				}
				return nil
			}
			if power > 0 {
				raised, err := got.Svc.SetFishingPower(int32(power)) //nolint:gosec // a byte the user typed
				if err != nil {
					return err
				}
				out["power"] = raised
				if !asJSON {
					printf(cmd, "[fishing] %s", raisedLine(raised, power))
				}
			}
			if !noKit {
				kit, err := got.Svc.FishingKit()
				if err != nil {
					return err
				}
				out["kit"] = kit
				if !asJSON {
					printf(cmd, "[fishing] %s", kitLine(kit.Gave))
				}
			}
			if watch {
				printf(cmd, "[fishing] watching — your bait will not run out. Ctrl-C to stop.")
				run, err := got.Svc.WatchBait(ctxOf(cmd), int32(keep), //nolint:gosec // a count the user typed
					seconds(interval), rounds, func(tick service.Tick) {
						printf(cmd, "%s", baitLine(tick))
					})
				if err != nil {
					return err
				}
				out["watch"] = run
				if !asJSON {
					printf(cmd, "[fishing] %d refill(s) over %d rounds", run.Refills, run.Rounds)
				}
			} else {
				tick, err := got.Svc.BaitTick(int32(keep)) //nolint:gosec // a count the user typed
				if err != nil {
					return err
				}
				out["bait"] = tick
				if !asJSON {
					printf(cmd, "%s", baitLine(tick))
				}
			}
			if asJSON {
				return printJSON(cmd, out)
			}
			return nil
		},
	}
	flags := cmd.Flags()
	flags.IntVar(&keep, "keep", 30,
		"top any bait stack below this back up to it (default 30)")
	flags.BoolVar(&noKit, "no-kit", false,
		"do not hand out a rod or bait, only keep bait topped up")
	flags.BoolVar(&watch, "watch", false, "keep topping bait up")
	flags.Float64Var(&interval, "interval", 1.0, "seconds between rounds with --watch")
	flags.IntVar(&rounds, "rounds", 0, "with --watch, stop after this many rounds")
	flags.IntVar(&power, "power", 0,
		"raise every rod you carry to this fishing power (1-255); the original "+
			"is recorded and put back by --restore")
	flags.BoolVar(&restore, "restore", false,
		"put every rod back to the power it had, and forget the record")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

// baitLine is what one bait round did.
func baitLine(tick service.Tick) string {
	topped, _ := tick["topped"].([]service.Topped)
	if len(topped) == 0 {
		if baits, _ := tick["baits"].(int); baits > 0 {
			return "[fishing] bait ok"
		}
		return "[fishing] no bait carried"
	}
	parts := make([]string, 0, len(topped))
	for _, t := range topped {
		parts = append(parts, fmt.Sprintf("slot %d %d->%d", t.Slot, t.Was, t.Now))
	}
	return "[fishing] topped up " + strings.Join(parts, ", ")
}

// restoredLine is what a rod-power restore put back.
func restoredLine(done service.Tick) string {
	rods, _ := done["restored"].([]service.Restored)
	if len(rods) == 0 {
		return "no rod power to put back"
	}
	parts := make([]string, 0, len(rods))
	for _, r := range rods {
		parts = append(parts, fmt.Sprintf("rod in slot %d back to power %d", r.Slot, r.Power))
	}
	return strings.Join(parts, ", ")
}

// raisedLine is which rods were raised, or that they were already there.
func raisedLine(raised service.Tick, power int) string {
	changed, _ := raised["changed"].([]service.PowerChange)
	if len(changed) == 0 {
		return fmt.Sprintf("rods already at power %d", power)
	}
	parts := make([]string, 0, len(changed))
	for _, c := range changed {
		parts = append(parts, fmt.Sprintf("slot %d %d -> %d", c.Slot, c.Was, c.Now))
	}
	return strings.Join(parts, ", ")
}

// kitLine is what the kit handed out, or that nothing was needed.
func kitLine(gave map[string]service.Given) string {
	if len(gave) == 0 {
		return "you already have a rod and bait"
	}
	parts := make([]string, 0, len(gave))
	for _, what := range []string{"rod", "bait"} {
		if g, ok := gave[what]; ok {
			parts = append(parts, fmt.Sprintf("%s (slot %d)", what, g.Slot))
		}
	}
	return "gave " + strings.Join(parts, ", ")
}

func (a *App) fishingBuffsCmd() *cobra.Command {
	var power, sonar, crate, watch, asJSON, force bool
	var rounds int
	var interval float64
	cmd := &cobra.Command{
		Use:   "fishing-buffs",
		Short: "fishing potion effects without the potions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			want := map[string]bool{}
			for _, e := range []struct {
				name string
				on   bool
			}{{"power", power}, {"sonar", sonar}, {"crate", crate}} {
				if e.on {
					want[e.name] = true
				}
			}
			if len(want) == 0 {
				printf(cmd, "[fishing] pick at least one of --power, --sonar, --crate")
				return &ExitError{Code: ExitFailure}
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if watch {
				printf(cmd, "[fishing] holding the fishing potion effects up. Ctrl-C to stop.")
				run, err := got.Svc.WatchFishingBuffs(ctxOf(cmd), want, 0,
					seconds(interval), rounds, func(tick service.Tick) {
						printf(cmd, "%s", buffLine(tick))
					})
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd, run)
				}
				printf(cmd, "[fishing] %d rounds", run.Rounds)
				return nil
			}
			tick, err := got.Svc.FishingBuffTick(want, 0)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			printf(cmd, "%s", buffLine(tick))
			return nil
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&power, "power", false, "Fishing Potion (fishing power +15)")
	flags.BoolVar(&sonar, "sonar", false, "Sonar Potion (see what is biting)")
	flags.BoolVar(&crate, "crate", false, "Crate Potion (more crates)")
	flags.BoolVar(&watch, "watch", false, "keep them up (otherwise a single round)")
	flags.Float64Var(&interval, "interval", 1.0, "seconds between rounds")
	flags.IntVar(&rounds, "rounds", 0, "with --watch, stop after this many rounds")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

// buffLine is which effects a round held, and which it left to a real potion.
func buffLine(tick service.Tick) string {
	held, _ := tick["held"].([]service.HeldBuff)
	deferred, _ := tick["deferred"].([]service.HeldBuff)
	var parts []string
	if len(held) > 0 {
		names := make([]string, 0, len(held))
		for _, b := range held {
			names = append(names, fmt.Sprintf("%s (%s)", b.Name, b.What))
		}
		parts = append(parts, "holding "+strings.Join(names, ", "))
	}
	if len(deferred) > 0 {
		names := make([]string, 0, len(deferred))
		for _, b := range deferred {
			names = append(names, b.Name)
		}
		parts = append(parts, "left "+strings.Join(names, ", ")+
			" alone — a potion is already running it")
	}
	if len(parts) == 0 {
		return "[fishing] nothing to hold"
	}
	return "[fishing] " + strings.Join(parts, "; ")
}
