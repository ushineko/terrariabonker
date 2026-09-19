package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/profile"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
Editing what is in a slot, and the two sweeps that edit every slot at once.
*/

func (a *App) setStackCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "set-stack SLOT VALUE",
		Short: "set an inventory slot's stack count",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slot, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("%q is not a slot", args[0])
			}
			value, err := atoi32(args[1])
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if err := got.Svc.SetStack(slot, value); err != nil {
				return err
			}
			printf(cmd, "[OK] set slot %d stack to %d", slot, value)
			return nil
		},
	}
	forceFlag(cmd, &force)
	return cmd
}

/*
itemFlags are the fields set-item can write.

Each is a pointer so "not given" is different from zero: a damage of nothing and
a damage of zero are different instructions, and conflating them rewrote fields
nobody asked about.
*/
type itemFlags struct {
	stack      int
	damage     int
	autoReuse  int
	useTime    int
	useAnim    int
	pick       int
	tileBoost  int
	defense    int
	prefix     int
	expectType int
}

func (a *App) setItemCmd() *cobra.Command {
	var force bool
	f := itemFlags{}
	cmd := &cobra.Command{
		Use: "set-item SLOT TYPE",
		Short: "edit a slot: type, stack, damage, auto-reuse, use-speed, pick, " +
			"tile-boost",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slot, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("%q is not a slot", args[0])
			}
			itemType, err := atoi32(args[1])
			if err != nil {
				return err
			}
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			edit, fields := f.edit(cmd)
			if err := got.Svc.SetItem(slot, itemType, edit); err != nil {
				return err
			}
			/*
				The edit is recorded so auto-restore can put it back. The game
				saves type, stack and prefix itself and regenerates the rest from
				the type on load, so only the regenerated fields are worth
				keeping -- and a slot set to nothing is an emptying, which is
				remembered as one rather than as an edit.
			*/
			if itemType != 0 {
				if _, err := got.Svc.RecordItemEdit(itemType, fields); err != nil {
					return err
				}
			} else if err := profile.ClearItem(slot); err != nil {
				return err
			}
			printf(cmd, "[OK] set slot %d to type %d", slot, itemType)
			return nil
		},
	}
	flags := cmd.Flags()
	flags.IntVar(&f.stack, "stack", 0, "how many")
	flags.IntVar(&f.damage, "damage", 0, "damage the item deals")
	flags.IntVar(&f.autoReuse, "auto-reuse", 0, "1 = auto-swing while held, 0 = off")
	flags.IntVar(&f.useTime, "use-time", 0, "ticks per use (lower=faster)")
	flags.IntVar(&f.useAnim, "use-anim", 0, "swing animation frames")
	flags.IntVar(&f.pick, "pick", 0, "pickaxe power (percent)")
	flags.IntVar(&f.tileBoost, "tile-boost", 0, "extra placement reach (tiles)")
	flags.IntVar(&f.defense, "defense", 0, "defense the item grants")
	flags.IntVar(&f.prefix, "prefix", 0,
		"modifier tier byte (0 none; e.g. Legendary/Warding/Menacing)")
	flags.IntVar(&f.expectType, "expect-type", 0,
		"ItemID the slot is believed to hold; refuse the write if it changed "+
			"in-game (guards against editing a stale snapshot)")
	forceFlag(cmd, &force)
	return cmd
}

/*
edit turns the flags into the fields to write, and the fields to remember.

Only the flags that were actually given are in either. A flag's zero value is a
real instruction here -- damage 0, auto-reuse 0 -- so "was it given" is asked of
the flag set rather than of the value.
*/
func (f itemFlags) edit(cmd *cobra.Command) (service.ItemEdit, map[string]float64) {
	var edit service.ItemEdit
	fields := map[string]float64{}
	given := func(name string) bool { return cmd.Flags().Changed(name) }

	num := func(name string, value int) *int32 {
		if !given(name) {
			return nil
		}
		out := int32(value) //nolint:gosec // a field the user typed
		return &out
	}
	remember := func(name, field string, value int) {
		if given(name) {
			fields[field] = float64(value)
		}
	}

	edit.Stack = num("stack", f.stack)
	edit.Damage = num("damage", f.damage)
	edit.UseTime = num("use-time", f.useTime)
	edit.UseAnim = num("use-anim", f.useAnim)
	edit.Pick = num("pick", f.pick)
	edit.TileBoost = num("tile-boost", f.tileBoost)
	edit.Defense = num("defense", f.defense)
	edit.Prefix = num("prefix", f.prefix)
	edit.ExpectType = num("expect-type", f.expectType)
	if given("auto-reuse") {
		on := f.autoReuse != 0
		edit.AutoReuse = &on
	}

	remember("damage", "damage", f.damage)
	remember("auto-reuse", "auto_reuse", f.autoReuse)
	remember("use-time", "use_time", f.useTime)
	remember("use-anim", "use_anim", f.useAnim)
	remember("pick", "pick", f.pick)
	remember("tile-boost", "tile_boost", f.tileBoost)
	remember("defense", "defense", f.defense)
	return edit, fields
}

func (a *App) giveCmd() *cobra.Command {
	var force bool
	var stack int
	cmd := &cobra.Command{
		Use:   "give TYPE",
		Short: "give an item into the first empty inventory slot",
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
			slot, err := got.Svc.GiveItem(itemType, int32(stack)) //nolint:gosec // a count the user typed
			if err != nil {
				return err
			}
			printf(cmd, "[OK] gave type %d x%d into slot %d", itemType, stack, slot)
			return nil
		},
	}
	cmd.Flags().IntVar(&stack, "stack", 1, "how many")
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) fastMiningCmd() *cobra.Command {
	var force bool
	var useTime, useAnim, pick int
	cmd := &cobra.Command{
		Use:   "fast-mining",
		Short: "speed up every pickaxe (persistent item edit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			hit, err := got.Svc.FastMining(int32(useTime), int32(useAnim), int32(pick)) //nolint:gosec // numbers the user typed
			if err != nil {
				return err
			}
			if len(hit) == 0 {
				printf(cmd, "[note] no pickaxe found (pick power > 0)")
				return nil
			}
			printf(cmd, "[OK] sped up pickaxe slots %v", hit)
			return nil
		},
	}
	cmd.Flags().IntVar(&useTime, "use-time", 8, "ticks per hit (default 8)")
	cmd.Flags().IntVar(&useAnim, "use-anim", 13, "swing frames (default 13)")
	cmd.Flags().IntVar(&pick, "pick", 200, "pickaxe power percent (default 200)")
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) longReachCmd() *cobra.Command {
	var force bool
	var tiles int
	cmd := &cobra.Command{
		Use:   "long-reach",
		Short: "extend placement reach on all items (Item.tileBoost)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			hit, err := got.Svc.LongReach(int32(tiles)) //nolint:gosec // a number the user typed
			if err != nil {
				return err
			}
			printf(cmd, "[OK] +%d placement reach on %d item(s). "+
				"Base reach (tileRangeX/Y) is frame-reset and needs the CE table.",
				tiles, len(hit))
			return nil
		},
	}
	cmd.Flags().IntVar(&tiles, "tiles", 20, "extra tiles of reach (default 20)")
	forceFlag(cmd, &force)
	return cmd
}
