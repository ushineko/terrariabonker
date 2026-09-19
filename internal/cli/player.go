package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The player: who they are, what they are made of, and what is in their pockets.
*/

// commands is the whole tree, in the order the help lists them.
func (a *App) commands() []*cobra.Command {
	return []*cobra.Command{
		a.statusCmd(), a.versionCmd(), a.inventoryCmd(),
		a.setHPCmd(), a.setMaxHPCmd(), a.setManaCmd(), a.setMaxManaCmd(),
		a.setStackCmd(), a.setItemCmd(), a.giveCmd(),
		a.fastMiningCmd(), a.longReachCmd(), a.freezeCmd(), a.godmodeCmd(),
		a.patchCmd(), a.restoreCmd(), a.buildCheckCmd(), a.acceptBuildCmd(),
		a.spawnNPCCmd(), a.compendiumCmd(), a.recipesCmd(), a.namesCmd(),
		a.prefixesCmd(),
		a.veinCmd(), a.extractCmd(), a.extractTickCmd(), a.extractStopCmd(),
		a.fishingCmd(), a.fishingBuffsCmd(), a.potionsCmd(),
		a.catchCmd(), a.catchTickCmd(), a.catchStopCmd(),
		a.projectileTickCmd(), a.projectileStopCmd(), a.projectileOfCmd(),
		a.sellCmd(), a.sellTickCmd(), a.sellListCmd(),
		a.extractRecipesCmd(), a.serveCmd(),
		a.readCmd(), a.writeCmd(),
	}
}

func (a *App) statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "find the player and show HP/mana",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			if !asJSON {
				printSnapshot(cmd, got.Svc, false)
				return nil
			}
			snap := got.Svc.Snapshot(false)
			reply := map[string]any{
				"pid": snap.PID, "version": snap.Version,
				"compat_level": snap.Level, "buildid": snap.BuildID,
				"build":  version.BuildKey(snap.Version, snap.BuildID),
				"copies": snap.Copies,
				"name":   nil, "hp": nil, "max_hp": nil, "mana": nil, "max_mana": nil,
				/*
					Which world is loaded, so the panel can re-apply the profile
					on a world switch and not only on a new pid. Null when it
					cannot be read.
				*/
				"world": worldOrNil(got.Svc),
			}
			if p := snap.Player; p != nil {
				reply["name"] = p.Name
				reply["hp"], reply["max_hp"] = p.HP, p.MaxHP
				reply["mana"], reply["max_mana"] = p.Mana, p.MaxMana
			}
			return printJSON(cmd, reply)
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

/*
worldOrNil is the loaded world, or nothing when there is none to read.

Written as [name, width, height] rather than as an object, which is the shape
the panel already stores and compares: it keeps the value verbatim and re-applies
the profile when it changes, so a reply that said the same thing in a different
shape would read as a world switch on the first poll.

The name carries the identity and the dimensions corroborate it. The dimensions
alone are not enough and neither is the tile buffer's address -- that was the
key once, and it is byte-identical across a switch between two worlds of the
same size.
*/
func worldOrNil(svc *service.Service) any {
	world, ok := svc.WorldID()
	if !ok {
		return nil
	}
	return []any{world.Name, world.Width, world.Height}
}

// printSnapshot is the human-readable status: the process, the player, the
// slots.
func printSnapshot(cmd *cobra.Command, svc *service.Service, showAll bool) {
	snap := svc.Snapshot(true)
	printf(cmd, "Terraria PID %d - %d player copy(ies) - build %s (%s)",
		snap.PID, snap.Copies, snap.Version, snap.Level)
	if p := snap.Player; p != nil {
		printf(cmd, "  %q: HP %d/%d  Mana %d/%d", p.Name, p.HP, p.MaxHP, p.Mana, p.MaxMana)
	}
	for _, s := range snap.Inventory {
		if s.Type == 0 && !showAll {
			continue
		}
		printf(cmd, "  %s", slotLine(s))
	}
}

// slotLine is one inventory row, the same shape in `status` and `inventory`.
func slotLine(s service.ItemSlot) string {
	var extra strings.Builder
	if s.Damage > 0 {
		fmt.Fprintf(&extra, " dmg=%d", s.Damage)
	}
	if s.AutoReuse != 0 {
		extra.WriteString(" auto")
	}
	if s.Pick > 0 {
		fmt.Fprintf(&extra, " useTime=%d pick=%d", s.UseTime, s.Pick)
	}
	return fmt.Sprintf("slot %2d: type=%-5d stack=%d%s", s.Slot, s.Type, s.Stack, extra.String())
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "report the game version and compatibility",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			snap := got.Svc.Snapshot(false)
			printf(cmd, "detected version : %s", snap.Version)
			printf(cmd, "detected buildid : %s", snap.BuildID)
			printf(cmd, "compatibility    : %s - %s", snap.Level, snap.Message)
			if snap.Level != version.Exact && snap.Level != version.Hotfix {
				// Not a failure: the report ran and its answer is "no". A script
				// wants to tell that from the command not working.
				return &ExitError{Code: 2}
			}
			return nil
		},
	}
}

func (a *App) inventoryCmd() *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:     "inventory",
		Aliases: []string{"inv"},
		Short:   "list inventory slots",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			slots, err := got.Svc.Inventory()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, slots)
			}
			printf(cmd, "%d slots", len(slots))
			for _, s := range slots {
				if s.Type == 0 && !all {
					continue
				}
				printf(cmd, "  %s", slotLine(s))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include empty slots")
	jsonFlag(cmd, &asJSON)
	return cmd
}

/*
setValueCmd is the four commands that set one number on the player.

They differ only in the name, the word in the message and the setter, and
writing them out four times is four places for one of them to be wired to the
wrong field.
*/
func (a *App) setValueCmd(use, short, what string,
	set func(*service.Service, string) error) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   use + " VALUE",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			if err := set(got.Svc, args[0]); err != nil {
				return err
			}
			printf(cmd, "[OK] set %s to %s", what, args[0])
			return nil
		},
	}
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) setHPCmd() *cobra.Command {
	return a.setValueCmd("set-hp", "set current HP (a number or 'max')", "HP",
		func(svc *service.Service, value string) error {
			if value == "max" {
				return svc.SetHPMax()
			}
			n, err := atoi32(value)
			if err != nil {
				return err
			}
			return svc.SetHP(n)
		})
}

func (a *App) setManaCmd() *cobra.Command {
	return a.setValueCmd("set-mana", "set current mana (a number or 'max')", "mana",
		func(svc *service.Service, value string) error {
			if value == "max" {
				return svc.SetManaMax()
			}
			n, err := atoi32(value)
			if err != nil {
				return err
			}
			return svc.SetMana(n)
		})
}

func (a *App) setMaxHPCmd() *cobra.Command {
	return a.setValueCmd("set-max-hp", "set permanent max HP", "max HP",
		func(svc *service.Service, value string) error {
			n, err := atoi32(value)
			if err != nil {
				return err
			}
			return svc.SetMaxHP(n)
		})
}

func (a *App) setMaxManaCmd() *cobra.Command {
	return a.setValueCmd("set-max-mana", "set permanent max mana", "max mana",
		func(svc *service.Service, value string) error {
			n, err := atoi32(value)
			if err != nil {
				return err
			}
			return svc.SetMaxMana(n)
		})
}

// atoi32 is a whole number, or a usage failure naming what was given.
func atoi32(s string) (int32, error) {
	var n int32
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, usagef("%q is not a number", s)
	}
	return n, nil
}
