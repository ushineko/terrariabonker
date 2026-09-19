/*
Command prefixstats regenerates data/prefix_stats.json from the game's own IL.

A modifier's bonuses are not a display value: Item.Prefix multiplies the item's fields by
these numbers and stores the results. The table has 82 entries and nine possible stats
each, which is exactly the sort of thing that gets transcribed with one digit wrong and
then believed -- so it is read out of Item::TryGetPrefixStatMultipliersForItem instead.

That method is a flat `if (prefix == N) { out_a = x; out_b = y; }` chain, which is why a
line-by-line parse is enough and no real IL interpretation is needed.

The out-parameters are mapped to fields by reading how Item::Prefix consumes them:

	arg2 damage   arg3 knockBack   arg4 useTime/useAnimation/reuseDelay   arg5 scale
	arg6 shootSpeed   arg7 mana     arg8 crit(+)   arg9 bonusTagDamage(+)  arg10 armorPen(+)

Usage:

	go run ./cmd/prefixstats [path/to/Terraria.exe] > data/prefix_stats.json
	go run ./cmd/prefixstats --il dump.txt > data/prefix_stats.json

The first form shells out to tools/ilrecon (dotnet) for the dump; the second reads one
that is already on disk.

Ported from tools/extract_prefix_stats.py (spec 051).
*/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultExe = "/mnt/Data3/SteamLibrary/steamapps/common/Terraria/Terraria.exe"
	method     = "Terraria.Item::TryGetPrefixStatMultipliersForItem"

	// Fewer than this many prefixes means the IL is not the shape this parse
	// assumes any more, which is a thing to be told about rather than to write
	// a short table over the good one.
	leastPrefixes = 50
)

// args maps an out-parameter to the item field it scales. Multiplicative unless it is
// additive.
var args = map[string]string{
	"ldarg.2":    "damage",
	"ldarg.3":    "knockback",
	"ldarg.s 4":  "usetime", // useAnimation, useTime and reuseDelay share this one
	"ldarg.s 5":  "scale",
	"ldarg.s 6":  "shootspeed",
	"ldarg.s 7":  "mana",
	"ldarg.s 8":  "crit",
	"ldarg.s 9":  "tagdamage",
	"ldarg.s 10": "armorpen",
}

// additive is the fields the game adds rather than multiplies, so their default is 0.
var additive = map[string]bool{"crit": true, "tagdamage": true, "armorpen": true}

var shortLoad = regexp.MustCompile(`^ldc\.i4\.(\d)$`)

// literal is the value an ldc.* op pushes, and whether it is one at all.
func literal(op string) (float64, bool) {
	for _, prefix := range []string{"ldc.r4 ", "ldc.i4 ", "ldc.i4.s "} {
		if strings.HasPrefix(op, prefix) {
			v, err := strconv.ParseFloat(op[strings.LastIndex(op, " ")+1:], 64)
			return v, err == nil
		}
	}
	if m := shortLoad.FindStringSubmatch(op); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil
	}
	return 0, false
}

// parse reads the table out of an ilrecon dump.
func parse(il string) map[int]map[string]float64 {
	var ops []string
	for _, line := range strings.Split(il, "\n") {
		if i := strings.Index(line, ": "); i >= 0 {
			ops = append(ops, strings.TrimSpace(line[i+2:]))
		}
	}

	table := map[int]map[string]float64{}
	current, open := 0, false
	pending := ""
	for i, op := range ops {
		// `ldarg.1 ; ldc.i4 N ; bne.un` is the test that opens one prefix's block.
		if strings.HasPrefix(op, "ldarg.1") && i+2 < len(ops) {
			if n, ok := literal(ops[i+1]); ok && strings.HasPrefix(ops[i+2], "bne") {
				current, open = int(n), true
				if table[current] == nil {
					table[current] = map[string]float64{}
				}
				continue
			}
		}
		if !open {
			continue
		}
		if field, ok := args[op]; ok {
			pending = field
			continue
		}
		if pending == "" {
			continue
		}
		if value, ok := literal(op); ok {
			// A multiplier of 1, or a bonus of 0, is the default and carries no
			// meaning.
			def := 1.0
			if additive[pending] {
				def = 0
			}
			if value != def {
				table[current][pending] = value
			}
		}
		pending = ""
	}

	for k, v := range table {
		if len(v) == 0 {
			delete(table, k)
		}
	}
	return table
}

// dumpIL asks ilrecon for the method's IL.
func dumpIL(exe string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "."
	}
	dir := filepath.Join(filepath.Dir(self), "tools", "ilrecon")
	if _, err := os.Stat(dir); err != nil {
		// Run from the checkout, which is how this is used: `go run` puts the
		// binary in a temporary directory, so the working directory is the
		// better guess.
		dir = filepath.Join("tools", "ilrecon")
	}
	// An explicit argv, no shell, and the only variable in it is the path to
	// Terraria.exe that the maintainer typed.
	// A first run builds ilrecon, which is minutes rather than seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dotnet", "run", "--", exe, "il", method) //nolint:gosec
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run ilrecon in %s: %w", dir, err)
	}
	return string(out), nil
}

/*
num is a multiplier as the file spells it.

A whole number keeps its ".0", which Go would otherwise drop: the file is regenerated
rather than edited, and a regeneration that rewrites every integral value is a diff nobody
can read past to see what actually changed.
*/
type num float64

// MarshalJSON writes the number with a fractional part either way.
func (n num) MarshalJSON() ([]byte, error) {
	out := strconv.FormatFloat(float64(n), 'f', -1, 64)
	if !strings.ContainsAny(out, ".eE") {
		out += ".0"
	}
	return []byte(out), nil
}

// render is the table as JSON, keyed by prefix id as a string.
func render(table map[int]map[string]float64) ([]byte, error) {
	byID := make(map[string]map[string]num, len(table))
	for id, stats := range table {
		row := make(map[string]num, len(stats))
		for field, v := range stats {
			row[field] = num(v)
		}
		byID[strconv.Itoa(id)] = row
	}
	raw, err := json.MarshalIndent(byID, "", " ")
	if err != nil {
		return nil, fmt.Errorf("encode the table: %w", err)
	}
	return append(raw, '\n'), nil
}

func main() {
	il := flag.String("il", "", "read an ilrecon dump from this file instead of running it")
	flag.Parse()

	dump := ""
	if *il != "" {
		raw, err := os.ReadFile(*il)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dump = string(raw)
	} else {
		exe := defaultExe
		if flag.NArg() > 0 {
			exe = flag.Arg(0)
		}
		got, err := dumpIL(exe)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dump = got
	}

	table := parse(dump)
	if len(table) < leastPrefixes {
		fmt.Fprintf(os.Stderr, "only %d prefixes parsed -- the IL shape has probably changed\n",
			len(table))
		os.Exit(1)
	}
	out, err := render(table)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
