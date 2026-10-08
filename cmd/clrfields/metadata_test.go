package main

import (
	"os"
	"testing"
)

/*
The parser against the real assembly, where it is installed (it is not in the
repo, so elsewhere this skips).

The literals were read from 1.4.5.8+24893155's Terraria.exe on 2026-10-08.
Entity starting at whoAmI is independent of this parser: it is the declaration
order cmd/monofields' Entity table lists. Pinning both ends and the count of
each class is what catches a field range shifted by one row, which keeps every
name present and moves every name to the wrong field -- the presence checks
alone pass against that.
*/
func TestFieldRangesFromTheRealAssembly(t *testing.T) {
	exe := `C:\Program Files (x86)\Steam\steamapps\common\Terraria\Terraria.exe`
	if _, err := os.Stat(exe); err != nil {
		t.Skip("game not installed")
	}
	types, err := LoadTypes(exe)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		n           int
		first, last string
		firstRID    uint32
	}{
		"Terraria.Entity": {14, "whoAmI", "lavaWet", 288},
		"Terraria.Player": {1223, "active", "_visualCloneReader", 1302},
	}
	byName := map[string]Type{}
	for _, ty := range types {
		byName[ty.FullName()] = ty
	}
	for name, w := range want {
		ty, ok := byName[name]
		if !ok {
			t.Errorf("no %s", name)
			continue
		}
		if len(ty.Fields) != w.n {
			t.Errorf("%s: %d fields, want %d", name, len(ty.Fields), w.n)
			continue
		}
		if f := ty.Fields[0]; f.Name != w.first || f.RID != w.firstRID {
			t.Errorf("%s first field %s (RID %d), want %s (RID %d)", name, f.Name, f.RID, w.first, w.firstRID)
		}
		if f := ty.Fields[len(ty.Fields)-1]; f.Name != w.last {
			t.Errorf("%s last field %s, want %s", name, f.Name, w.last)
		}
	}
	names := map[string]bool{}
	for _, f := range byName["Terraria.Player"].Fields {
		names[f.Name] = true
	}
	for _, want := range []string{"statLife", "statLifeMax", "statManaMax2", "name", "inventory", "pickSpeed", "blockRange"} {
		if !names[want] {
			t.Errorf("Player has no %s", want)
		}
	}
}
