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
	require.Equal(t, "ed275d4126d09034e0d942154d7effb910792899f04ab18632f85f5f968a5822", hex.EncodeToString(sum[:]), "the CLR table changed:\n%s", b.String())
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
