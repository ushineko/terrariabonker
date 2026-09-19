package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/game"
	"github.com/ushineko/terrariabonker/internal/recipes"
)

/*
The bundled tables, dumped as they are.

All of these exist for one reason: the window used to reach the data by
importing it in-process, which a front end in another language cannot do. The
tables are handed over unchanged rather than reshaped, so there is one spelling
of a recipe or a name and it is the file's.

They need no game and no privilege, which is the point -- a recipe book that
only works while Terraria is running is a recipe book nobody opens.
*/

func (a *App) compendiumCmd() *cobra.Command {
	var asJSON, refresh bool
	cmd := &cobra.Command{
		Use:   "compendium",
		Short: "dump the full item/NPC catalog as JSON (for the GUI tab)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			cat, err := got.Svc.Compendium(refresh)
			if err != nil {
				return err
			}
			// Always JSON: this is a data feed, not a listing.
			return printJSON(cmd, cat)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable (always on)")
	cmd.Flags().BoolVar(&refresh, "refresh", false,
		"rescan the game instead of using the per-build cache")
	return cmd
}

func (a *App) recipesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "recipes",
		Short: "dump the cached crafting recipes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			book, err := game.CraftingBook()
			if err != nil {
				return err
			}
			return printJSON(cmd, map[string]any{
				"recipes": book.Recipes, "stations": book.Stations,
				"tileicons": book.TileIcons,
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable catalog")
	return cmd
}

func (a *App) namesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "names",
		Short: "dump every ItemID with its display name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := game.ItemNames()
			if err != nil {
				return err
			}
			out := map[string]string{}
			for id, name := range names.All() {
				out[strconv.Itoa(id)] = name
			}
			return printJSON(cmd, out)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable catalog")
	return cmd
}

func (a *App) prefixesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "prefixes",
		Short: "list item modifiers (id, name, quality)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			px, err := game.ItemPrefixes()
			if err != nil {
				return err
			}
			out := []map[string]any{}
			for _, id := range px.All() {
				out = append(out, map[string]any{
					"id": id, "name": px.Name(id), "quality": px.Quality(id),
				})
			}
			return printJSON(cmd, out)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable catalog")
	return cmd
}

func (a *App) extractRecipesCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "extract-recipes",
		Short: "read Main.recipe[] from the running game -> data/recipes.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			mem, ok := got.Svc.Mem.(recipes.Mem)
			if !ok {
				return usagef("this game's memory cannot be read for recipes")
			}
			book, err := recipes.Extract(mem)
			if err != nil {
				return err
			}
			path := recipes.DataPath()
			if err := recipes.Save(book, path); err != nil {
				return err
			}
			printf(cmd, "[OK] extracted %d recipes, %d station names -> %s",
				len(book.Recipes), len(book.Stations), path)
			return nil
		},
	}
	forceFlag(cmd, &force)
	return cmd
}
