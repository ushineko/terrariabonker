package layout

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
The CLR table is the measurement, field for field.

docs/clr-fields-1.4.5.8.txt is what cmd/clrfields printed against the live game:
the provenance, written by the runtime and not by hand. Every entry here is
looked up in it by class and name, so a number mistyped into the table, or a
field filed under the wrong class or kind, fails rather than standing in for a
measurement it does not match.
*/
func TestTheCLRTableIsTheMeasurement(t *testing.T) {
	measured := readClrfieldsDump(t, "../../docs/clr-fields-1.4.5.8.txt")
	for class, fields := range CLRFields {
		for _, f := range fields {
			got, ok := measured[class][f.Name]
			if !ok {
				t.Errorf("%s.%s is not in the measurement", class, f.Name)
				continue
			}
			require.Equal(t, f, got, "%s.%s", class, f.Name)
		}
	}
}

/*
The whole CLR table, frozen. Changing it means changing this digest in the same
commit, with the measurement that justified the change.
*/
func TestTheCLRTableIsFrozen(t *testing.T) {
	classes := make([]string, 0, len(CLRFields))
	for c := range CLRFields {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	var b strings.Builder
	for _, c := range classes {
		fmt.Fprintf(&b, "%s:%#v\n", c, CLRFields[c])
	}
	sum := sha256.Sum256([]byte(b.String()))
	require.Equal(t, "7e88d8e830378b74b07737aa7f575f656f331543751aacb2c313e61968a362f5", hex.EncodeToString(sum[:]), "the CLR table changed:\n%s", b.String())
}

/*
The CLR life block is the order the shapes say, and contiguous.

locate.ValidBlock reads six consecutive ints; the first two are statLifeMax then
statLifeMax2, under the CLR as under mono. The shape and the table are
two statements of one measurement, so they are checked against each other.
*/
func TestTheCLRLifeBlockMatchesItsShape(t *testing.T) {
	at := map[string]uint32{}
	for _, f := range CLRFields["Player"] {
		at[f.Name] = f.Offset
	}
	order := []string{"statLifeMax2", "statLifeMax", "statLife", "statMana", "statManaMax", "statManaMax2"}
	if clrEntry.Shapes.LifeMaxFirst {
		order[0], order[1] = order[1], order[0]
	}
	for i := 1; i < len(order); i++ {
		require.Equal(t, at[order[0]]+uint32(4*i), at[order[i]], "%s is not where the block puts it", order[i])
	}
}

// readClrfieldsDump parses cmd/clrfields' printed output: "== Terraria.X: ..."
// headers, then "inst|static 0xOFF et=0xNN name" lines.
func readClrfieldsDump(t *testing.T, path string) map[string]map[string]CLRField {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // a checked-in measurement
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	out := map[string]map[string]CLRField{}
	class := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "== Terraria."); ok {
			class, _, _ = strings.Cut(rest, ":")
			out[class] = map[string]CLRField{}
			continue
		}
		p := strings.Fields(line)
		if class == "" || len(p) != 4 || (p[0] != "inst" && p[0] != "static") {
			continue
		}
		off, err := strconv.ParseUint(strings.TrimPrefix(p[1], "0x"), 16, 32)
		require.NoError(t, err, line)
		out[class][p[3]] = CLRField{Name: p[3], Offset: uint32(off), Static: p[0] == "static"}
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, out["Player"], "the measurement has no Player section")
	return out
}

/*
The CLR copy span holds no reference, and ends where the object does.

A type change copies [CopyLo, CopyHi) out of the game's pristine template over the
slot's item. A reference inside that span would copy one object's pointer into
another; a span past the end of the object would copy whatever follows it on the
heap. So: every reference field in the measured Item layout ends at or before
CopyLo, and CopyLo is exactly the end of the last one; every field starts before
CopyHi; and CopyHi is the object's end as the runtime states it -- the Item
MethodTable's BaseSize, 0x148 on the live game (2026-10-08), less the 4-byte sync
block in front of the object pointer.
*/
func TestTheCLRCopySpanHoldsNoReference(t *testing.T) {
	fields := measuredInstanceFields(t, "Item")
	lastRefEnd := 0
	for _, fa := range fields {
		if isCLRReference[fa.et] {
			lastRefEnd = max(lastRefEnd, fa.off+4)
		}
		require.Less(t, fa.off, clrEntry.Item.CopyHi, "a field starts past the copy span's end")
	}
	require.Equal(t, lastRefEnd, clrEntry.Item.CopyLo, "the span does not start right after the last reference")
	require.Equal(t, 0x148-4, clrEntry.Item.CopyHi, "the span does not end with the object")
}

// measuredField is one instance field of the measurement: its name, offset and
// CorElementType.
type measuredField struct {
	name string
	off  int
	et   int
}

// isCLRReference is the CorElementType values that are references: string,
// class, var, generic instance (conservatively), array, object, szarray.
var isCLRReference = map[int]bool{0x0E: true, 0x12: true, 0x13: true, 0x14: true, 0x15: true, 0x1C: true, 0x1D: true}

// measuredInstanceFields is every instance field the measurement lists under the
// given classes.
func measuredInstanceFields(t *testing.T, classes ...string) []measuredField {
	t.Helper()
	f, err := os.Open("../../docs/clr-fields-1.4.5.8.txt") //nolint:gosec // a checked-in measurement
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var fields []measuredField
	wanted := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "== Terraria."); ok {
			class, _, _ := strings.Cut(rest, ":")
			wanted = false
			for _, c := range classes {
				wanted = wanted || c == class
			}
			continue
		}
		p := strings.Fields(line)
		if !wanted || len(p) != 4 || p[0] != "inst" {
			continue
		}
		off, err := strconv.ParseUint(strings.TrimPrefix(p[1], "0x"), 16, 32)
		require.NoError(t, err)
		et, err := strconv.ParseUint(strings.TrimPrefix(p[2], "et=0x"), 16, 8)
		require.NoError(t, err)
		fields = append(fields, measuredField{p[3], int(off), int(et)})
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, fields)
	return fields
}

/*
The CLR spawn spans hold no reference, leave the slot what is its own, and carry
everything else.

A spawn copies each span out of the game's template of an NPC over a Main.npc
slot. A reference inside a span would hand the slot the template's own array, and
the two would share it from then on. So, against the measured Entity and NPC
layouts: no reference field starts inside a span; whoAmI and the four Vector2s
from position to oldVelocity -- which the spawn writes itself or leaves -- are
outside every span; every other instance field is inside one, so nothing of the
template is left behind; and the last span ends where the object does, the NPC
MethodTable's BaseSize 0x210 on the live game (2026-10-11) less the 4-byte sync
block.
*/
func TestTheCLRSpawnSpansHoldNoReference(t *testing.T) {
	spans := clrEntry.Spawn.CopySpans
	require.NotEmpty(t, spans)
	inSpan := func(off int) bool {
		for _, s := range spans {
			if off >= s[0] && off < s[1] {
				return true
			}
		}
		return false
	}
	own := map[string]bool{"whoAmI": true, "position": true, "velocity": true,
		"oldPosition": true, "oldVelocity": true}
	refs := 0
	for _, f := range measuredInstanceFields(t, "Entity", "NPC") {
		switch {
		case isCLRReference[f.et]:
			refs++
			require.Falsef(t, inSpan(f.off), "the reference %s (%#x) is inside a copy span", f.name, f.off)
		case own[f.name]:
			require.Falsef(t, inSpan(f.off), "%s (%#x) is the slot's own and is inside a copy span", f.name, f.off)
			delete(own, f.name)
		default:
			require.Truef(t, inSpan(f.off), "%s (%#x) is in no copy span", f.name, f.off)
		}
	}
	require.Empty(t, own, "an Entity field the spawn keeps was not in the measurement")
	require.Equal(t, 14, refs, "the measurement lists a different number of NPC references")

	prev := 4 // past the MethodTable pointer
	for _, s := range spans {
		require.GreaterOrEqual(t, s[0], prev, "the spans overlap, are out of order, or reach the header")
		require.Greater(t, s[1], s[0])
		prev = s[1]
	}
	require.Equal(t, 0x210-4, prev, "the last span does not end with the object")
	require.Equal(t, clrEntry.NPC.Size, prev, "the spans and the NPC shape disagree about the object's end")
}
