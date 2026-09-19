package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/tiles"
)

/*
Vein mining: what a tile would take, taking it, and watching for the player
taking one by hand.
*/

// coords are the optional tile arguments both vein commands accept.
func coords(args []string) (x, y int32, given bool, err error) {
	if len(args) < 2 {
		return 0, 0, false, nil
	}
	ix, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, 0, false, usagef("%q is not a tile x", args[0])
	}
	iy, err := strconv.Atoi(args[1])
	if err != nil {
		return 0, 0, false, usagef("%q is not a tile y", args[1])
	}
	return int32(ix), int32(iy), true, nil //nolint:gosec // coordinates the user typed
}

func (a *App) veinCmd() *cobra.Command {
	var gems, orthogonal, drawMap, asJSON bool
	var limit int
	cmd := &cobra.Command{
		Use:   "vein [X Y]",
		Short: "dry run: what a vein miner would take from a tile (reads only)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			got, err := a.game(false, false)
			if err != nil {
				return err
			}
			x, y, given, err := coords(args)
			if err != nil {
				return err
			}
			if !given {
				if x, y, err = got.Svc.PlayerTile(); err != nil {
					return err
				}
				printf(cmd, "[vein] no coordinates given; using the player's tile (%d, %d)", x, y)
			}
			vein, err := got.Svc.VeinAt(x, y, gems, limit, !orthogonal)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, vein)
			}
			if !vein.Whitelisted {
				printf(cmd, "[vein] tile (%d, %d) is id %s — not on the whitelist; "+
					"nothing would be mined", x, y, tileID(vein.Type))
				return nil
			}
			capped := ""
			if vein.Capped {
				capped = "  [CAPPED — the vein is larger]"
			}
			printf(cmd, "[vein] %s (id %s) at (%d, %d): %d tiles would be mined%s",
				vein.Name, tileID(vein.Type), x, y, vein.Count, capped)
			lo, hi := boundingBox(vein.Tiles)
			printf(cmd, "       bounding box x %d..%d, y %d..%d", lo[0], hi[0], lo[1], hi[1])
			if drawMap {
				return a.drawVein(cmd, got, vein, lo, hi)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&gems, "gems", false, "include gems as well as ores")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many tiles")
	cmd.Flags().BoolVar(&orthogonal, "orthogonal", false,
		"only up/down/left/right, not diagonals")
	cmd.Flags().BoolVar(&drawMap, "map", false, "draw the vein")
	jsonFlag(cmd, &asJSON)
	return cmd
}

// tileID is a tile's number, or the word for a tile that is not there.
func tileID(id *uint16) string {
	if id == nil {
		return "none"
	}
	return strconv.Itoa(int(*id))
}

// boundingBox is the corners of a set of tiles.
func boundingBox(tiles [][2]int32) (lo, hi [2]int32) {
	if len(tiles) == 0 {
		return lo, hi
	}
	lo, hi = tiles[0], tiles[0]
	for _, t := range tiles[1:] {
		for i := range 2 {
			lo[i] = min(lo[i], t[i])
			hi[i] = max(hi[i], t[i])
		}
	}
	return lo, hi
}

// drawVein draws the flood over the tiles around it, which is how a person
// checks that the whitelist took what they meant.
func (a *App) drawVein(cmd *cobra.Command, got *Game, vein service.Vein,
	lo, hi [2]int32) error {
	tm, err := got.Svc.TileMap()
	if err != nil {
		return err
	}
	taken := map[[2]int32]bool{}
	for _, t := range vein.Tiles {
		taken[t] = true
	}
	for y := lo[1] - 1; y <= hi[1]+1; y++ {
		row := make([]byte, 0, hi[0]-lo[0]+3)
		for x := lo[0] - 1; x <= hi[0]+1; x++ {
			switch {
			case taken[[2]int32{x, y}]:
				row = append(row, '#')
			case typeThere(tm, x, y):
				row = append(row, '.')
			default:
				row = append(row, ' ')
			}
		}
		printf(cmd, "       %s", row)
	}
	return nil
}

// extractLine is one extraction as the command line reports it.
func extractLine(got service.Extracted) string {
	out := fmt.Sprintf("[extract] %d of %d tiles mined at (%d, %d)",
		got.Mined, got.Queued, got.At[0], got.At[1])
	if got.MedianWait != nil {
		out += fmt.Sprintf(", median %.2fs/tile", *got.MedianWait)
	}
	if got.Reason != "" {
		out += " — " + got.Reason
	}
	return out
}

func (a *App) extractCmd() *cobra.Command {
	var gems, asJSON, force, watch bool
	var limit, rounds int
	var timeout float64
	cmd := &cobra.Command{
		Use:   "extract [X Y]",
		Short: "mine the vein at a tile (WRITES to the world)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			wait := seconds(timeout)
			if watch {
				printf(cmd, "[extract] watching — break one ore by hand and the rest "+
					"of its vein goes with it. Ctrl-C to stop.")
				run, err := got.Svc.WatchVeins(ctxOf(cmd), got.Patcher, gems, limit, 0,
					wait, rounds, func(e service.Extracted) {
						printf(cmd, "%s", extractLine(e))
					})
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd, run)
				}
				printf(cmd, "[extract] %d tiles over %d vein(s)", run.Mined, len(run.Events))
				return nil
			}
			x, y, given, err := coords(args)
			if err != nil {
				return err
			}
			if !given {
				if x, y, err = got.Svc.PlayerTile(); err != nil {
					return err
				}
			}
			run, err := got.Svc.ExtractVein(got.Patcher, x, y, gems, limit, wait)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, run)
			}
			printf(cmd, "%s", extractLine(run))
			return nil
		},
	}
	cmd.Flags().BoolVar(&gems, "gems", false, "include gems")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many tiles")
	cmd.Flags().Float64Var(&timeout, "timeout", 20.0,
		"seconds to wait for each tile before giving up")
	cmd.Flags().BoolVar(&watch, "watch", false,
		"keep watching: break one ore by hand and its vein goes with it")
	cmd.Flags().IntVar(&rounds, "rounds", 0,
		"with --watch, stop after this many polls (default: forever)")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) extractTickCmd() *cobra.Command {
	var gems, asJSON, force bool
	var limit int
	var timeout, budget float64
	cmd := &cobra.Command{
		Use:   "extract-tick",
		Short: "one slice of vein watching (GUI; WRITES to the world)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			run, err := got.Svc.WatchTick(got.Patcher, gems, limit,
				seconds(timeout), seconds(budget))
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd, run)
			}
			for _, e := range run.Events {
				printf(cmd, "%s", extractLine(e))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&gems, "gems", false, "include gems")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many tiles")
	cmd.Flags().Float64Var(&timeout, "timeout", 8.0,
		"seconds to wait for a batch to break")
	cmd.Flags().Float64Var(&budget, "budget", 0.08,
		"seconds to spend watching in this call")
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

func (a *App) extractStopCmd() *cobra.Command {
	var asJSON, force bool
	cmd := &cobra.Command{
		Use:   "extract-stop",
		Short: "drop the vein watcher and disarm",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, err := a.game(true, force)
			if err != nil {
				return err
			}
			stopped := got.Svc.WatchStop()
			if asJSON {
				return printJSON(cmd, map[string]bool{"stopped": stopped})
			}
			printf(cmd, "[extract] watcher stopped")
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	forceFlag(cmd, &force)
	return cmd
}

// typeThere reports whether a tile has anything in it, which is the difference
// between the map's "." and its blank.
func typeThere(tm *tiles.TileMap, x, y int32) bool {
	id, ok := tm.TypeAt(x, y)
	return ok && id != 0
}

// seconds is a flag's float as a duration.
func seconds(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }

// ctxOf is the command's context, which carries the interrupt that ends a watch
// loop.
func ctxOf(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
