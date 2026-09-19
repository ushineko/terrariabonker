package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/selling"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Selling, favorited potions, and the two debug pokes.

Selling is the one operation here with no undo, which is why the whitelist is
opt-in per item type, why a dry run exists at all, and why the round reports
what it took rather than only that it ran.
*/

// coins is copper as the game would write it: 1 platinum 23 gold 45 silver.
func coins(copper int32) string {
	stacks := selling.CoinStacks(copper)
	if len(stacks) == 0 {
		return "0 copper"
	}
	names := map[int32]string{71: "copper", 72: "silver", 73: "gold", 74: "platinum"}
	parts := make([]string, 0, len(stacks))
	for _, s := range stacks {
		parts = append(parts, fmt.Sprintf("%d %s", s.Stack, names[s.Type]))
	}
	return strings.Join(parts, " ")
}

// sellLine is what one selling round did.
func sellLine(tick service.Tick) string {
	if failed, _ := tick["error"].(string); failed != "" {
		return "[sell] " + failed
	}
	sold, _ := tick["sold"].([]service.Sold)
	if len(sold) == 0 {
		skipped, _ := tick["skipped"].([]service.Skipped)
		if len(skipped) > 0 {
			return fmt.Sprintf("[sell] nothing to sell (%d skipped)", len(skipped))
		}
		return "[sell] nothing to sell"
	}
	parts := make([]string, 0, len(sold))
	for _, s := range sold {
		parts = append(parts, fmt.Sprintf("%dx %s", s.Stack, s.Name))
	}
	where := map[string]string{
		"bank": "the piggy bank", "inventory": "your inventory",
		"bank+inventory": "the piggy bank and your inventory",
	}
	dest, _ := tick["destination"].(string)
	copper, _ := tick["copper"].(int32)
	return fmt.Sprintf("[sell] %s -> %s into %s",
		strings.Join(parts, ", "), coins(copper), where[dest])
}

func (a *App) sellCmd() *cobra.Command {
	var list, dryRun, watch, asJSON, force bool
	var add, remove []int
	var rounds int
	var interval float64
	cmd := &cobra.Command{
		Use:   "sell",
		Short: "sell whitelisted items for coins (PERMANENT)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := editWhitelist(add, remove); err != nil {
				return err
			}
			if list {
				return a.printWhitelist(cmd, asJSON)
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if watch {
				printf(cmd, "[sell] watching — whitelisted items are sold as they "+
					"arrive. Ctrl-C stops.")
				run, err := got.Svc.WatchSelling(ctxOf(cmd), seconds(interval), rounds,
					func(tick service.Tick) { printf(cmd, "%s", sellLine(tick)) })
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd, run)
				}
				printf(cmd, "[sell] %s over %d rounds", coins(run.Copper), run.Rounds)
				return nil
			}
			tick, err := got.Svc.SellTick(dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			printf(cmd, "%s", sellLine(tick))
			return nil
		},
	}
	flags := cmd.Flags()
	flags.IntSliceVar(&add, "add", nil, "add item type(s) to the whitelist")
	flags.IntSliceVar(&remove, "remove", nil, "remove item type(s) from the whitelist")
	flags.BoolVar(&list, "list", false, "show the whitelist and stop")
	flags.BoolVar(&dryRun, "dry-run", false, "report what would sell, and sell nothing")
	flags.BoolVar(&watch, "watch", false,
		"keep selling as items arrive (otherwise a single round)")
	flags.Float64Var(&interval, "interval", 0.5, "seconds between rounds when watching")
	flags.IntVar(&rounds, "rounds", 0,
		"with --watch, stop after this many rounds (default: forever)")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) sellTickCmd() *cobra.Command {
	var dryRun, asJSON, force bool
	cmd := &cobra.Command{
		Use:   "sell-tick",
		Short: "one auto-sell round (GUI; PERMANENT)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			tick, err := got.Svc.SellTick(dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			printf(cmd, "%s", sellLine(tick))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would sell, and sell nothing")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) sellListCmd() *cobra.Command {
	var asJSON, force bool
	var add, remove []int
	cmd := &cobra.Command{
		Use:   "sell-list",
		Short: "the auto-sell whitelist (GUI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := editWhitelist(add, remove); err != nil {
				return err
			}
			return a.printWhitelist(cmd, asJSON)
		},
	}
	cmd.Flags().IntSliceVar(&add, "add", nil, "add item type(s) to the whitelist")
	cmd.Flags().IntSliceVar(&remove, "remove", nil, "remove item type(s) from the whitelist")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

// editWhitelist applies the --add and --remove flags, which both commands take.
func editWhitelist(add, remove []int) error {
	for _, t := range add {
		if err := profile.SetSellWhitelist(int32(t), true); err != nil { //nolint:gosec // an item type
			return err
		}
	}
	for _, t := range remove {
		if err := profile.SetSellWhitelist(int32(t), false); err != nil { //nolint:gosec // an item type
			return err
		}
	}
	return nil
}

// printWhitelist shows the list, which needs no game: it is the player's own
// saved setting.
func (a *App) printWhitelist(cmd *cobra.Command, asJSON bool) error {
	list := sellWhitelist()
	if asJSON {
		names := map[string]string{}
		for _, t := range list {
			names[fmt.Sprint(t)] = itemLabel(t)
		}
		return printJSON(cmd, map[string]any{"whitelist": list, "names": names})
	}
	printf(cmd, "[sell] whitelist: %s", sellListing(func(id int) string {
		return itemLabel(int32(id)) //nolint:gosec // an item type
	}, list))
	return nil
}

// itemLabel is an item's name, or its number when there is none.
func itemLabel(id int32) string {
	names, err := game.ItemNames()
	if err != nil {
		return fmt.Sprint(id)
	}
	return names.Label(int(id))
}

// potionLine is what one potion round did.
func potionLine(tick service.Tick) string {
	var parts []string
	for _, kind := range []string{"added", "renewed", "kept"} {
		rows, _ := tick[kind].([]map[string]int32)
		if len(rows) == 0 {
			continue
		}
		parts = append(parts, kind+" "+buffIDs(rows))
	}
	if rows, _ := tick["full"].([]map[string]int32); len(rows) > 0 {
		parts = append(parts, "NO FREE BUFF SLOT for "+buffIDs(rows))
	}
	if len(parts) == 0 {
		return "[potions] nothing favorited"
	}
	return "[potions] " + strings.Join(parts, "; ")
}

// buffIDs is the buff numbers in a row set, comma separated.
func buffIDs(rows []map[string]int32) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprint(row["buff"]))
	}
	return strings.Join(parts, ",")
}

func (a *App) potionsCmd() *cobra.Command {
	var watch, asJSON, force bool
	var minStack, ticks, rounds int
	var interval float64
	cmd := &cobra.Command{
		Use:   "potions",
		Short: "favorited potions grant their buff from the inventory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if watch {
				printf(cmd, "[potions] watching — favorite a potion (alt-click) to "+
					"switch it on, unfavorite or drop it to let it lapse. Ctrl-C to stop.")
				run, err := got.Svc.WatchPotions(ctxOf(cmd), int32(minStack), int32(ticks), //nolint:gosec // numbers the user typed
					seconds(interval), rounds,
					func(tick service.Tick) { printf(cmd, "%s", potionLine(tick)) })
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd, run)
				}
				printf(cmd, "[potions] %d applied over %d rounds", run.Applied, run.Rounds)
				return nil
			}
			tick, err := got.Svc.PotionTick(int32(minStack), int32(ticks)) //nolint:gosec // numbers the user typed
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, tick)
			}
			printf(cmd, "%s", potionLine(tick))
			return nil
		},
	}
	flags := cmd.Flags()
	flags.IntVar(&minStack, "min-stack", 1,
		"only potions with at least this many in the stack (default 1)")
	flags.IntVar(&ticks, "ticks", 0,
		"buff time written per round, in frames (60 = 1s)")
	flags.Float64Var(&interval, "interval", 0.25,
		"seconds between rounds; must be well under --ticks")
	flags.BoolVar(&watch, "watch", false,
		"keep them topped up (otherwise a single round)")
	flags.IntVar(&rounds, "rounds", 0,
		"with --watch, stop after this many rounds (default: forever)")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) readCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read ADDR",
		Short: "read an int32 at a hex address (debug)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := hexAddr(args[0])
			if err != nil {
				return err
			}
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			value, _ := got.Svc.Mem.ReadI32(addr)
			printf(cmd, "%d", value)
			return nil
		},
	}
}

func (a *App) writeCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "write ADDR VALUE",
		Short: "write an int32 at a hex address (debug)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := hexAddr(args[0])
			if err != nil {
				return err
			}
			value, err := atoi32(args[1])
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if !got.Svc.Mem.WriteI32(addr, value) {
				printf(cmd, "[FAIL] wrote %d to %s", value, args[0])
				return &ExitError{Code: ExitFailure}
			}
			printf(cmd, "[OK] wrote %d to %s", value, args[0])
			return nil
		},
	}
	forceFlag(cmd, &force)
	return cmd
}

// hexAddr is an address as the debug commands take it.
func hexAddr(s string) (uint32, error) {
	var addr uint64
	if _, err := fmt.Sscanf(strings.TrimPrefix(s, "0x"), "%x", &addr); err != nil {
		return 0, usagef("%q is not a hex address", s)
	}
	return uint32(addr), nil //nolint:gosec // a 32-bit process
}
