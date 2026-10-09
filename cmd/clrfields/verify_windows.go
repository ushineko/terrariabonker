//go:build windows

package main

import (
	"fmt"
	"sort"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
verify checks internal/layout's CLR table against the running game, and returns
the exit code: 0 when every pinned field is where the runtime says, 1 otherwise.

A class counts only when the scan found exactly one complete FieldDesc list for
it. None means the class is not loaded or the model no longer fits this runtime;
two would mean a coincidence the stored-field count failed to rule out. Either
way there is no single answer to check against, and that is a failure, not a
skip -- "unverified" must not quietly read as "fine".
*/
func verify(types []Type, byRID map[uint32]owner, runs map[int][][]record) int {
	byName := map[string]int{}
	for i, t := range types {
		byName[t.FullName()] = i
	}
	classes := make([]string, 0, len(layout.CLRFields))
	for c := range layout.CLRFields {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	bad := 0
	for _, class := range classes {
		ti, ok := byName["Terraria."+class]
		if !ok {
			fmt.Printf("== %s: no such TypeDef in the assembly\n", class)
			bad++
			continue
		}
		t := types[ti]
		stored := 0
		for _, f := range t.Fields {
			if !f.Literal {
				stored++
			}
		}
		var complete [][]record
		for _, run := range runs[ti] {
			if len(run) == stored {
				complete = append(complete, run)
			}
		}
		if len(complete) != 1 {
			fmt.Printf("== %s: %d complete FieldDesc lists, need exactly 1\n", class, len(complete))
			bad++
			continue
		}
		got := map[string]layout.CLRField{}
		for _, r := range complete[0] {
			name := t.Fields[byRID[r.rid].field].Name
			off := r.off
			if !r.static {
				off += objectHeader
			}
			got[name] = layout.CLRField{Name: name, Offset: off, Static: r.static}
		}
		fmt.Printf("== %s\n", class)
		for _, want := range layout.CLRFields[class] {
			have, ok := got[want.Name]
			switch {
			case !ok:
				fmt.Printf("  MISSING  %-20s want %#05x\n", want.Name, want.Offset)
				bad++
			case have != want:
				fmt.Printf("  DIFFERS  %-20s want %#05x static=%t, runtime says %#05x static=%t\n",
					want.Name, want.Offset, want.Static, have.Offset, have.Static)
				bad++
			default:
				fmt.Printf("  ok       %-20s %#05x\n", want.Name, want.Offset)
			}
		}
	}
	if bad > 0 {
		fmt.Printf("\n%d disagreement(s) with internal/layout's CLR table\n", bad)
		return 1
	}
	fmt.Println("\nevery CLR field in internal/layout agrees with the runtime")
	return 0
}
