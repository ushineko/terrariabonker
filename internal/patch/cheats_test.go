package patch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/patch"
)

/*
The catalog is compared whole, in both directions.

Every field of it reaches the game or the user: the anchor and offset decide
where bytes are written, the original bytes are what disabling puts back, and the
label and note are what somebody reads before turning a cheat on. A patch that
went missing from the table would not fail -- it would simply stop being offered,
and the one already installed in a running game would have nothing left that
knows how to remove it.
*/
func TestEveryCheatMatchesThePython(t *testing.T) {
	var want map[string]struct {
		Label    string  `json:"label"`
		Anchor   string  `json:"anchor"`
		PatchOff int     `json:"patch_off"`
		Orig     string  `json:"orig"`
		Patched  string  `json:"patched"`
		ValueOff int     `json:"value_off"`
		Kind     string  `json:"value_kind"`
		OnValue  float64 `json:"on_value"`
		OffValue float64 `json:"off_value"`
		Note     string  `json:"note"`
		Tunable  bool    `json:"tunable"`
	}
	askPython(t, `
import json
from terrariabonker import patcher as P
print(json.dumps({n: {
    "label": c.label, "anchor": c.anchor, "patch_off": c.patch_off,
    "orig": c.orig.hex(), "patched": c.patched.hex(),
    "value_off": c.value_off or 0, "value_kind": c.value_kind,
    "on_value": float(c.on_value), "off_value": float(c.off_value),
    "note": c.note, "tunable": c.make_patched is not None,
} for n, c in P.CHEATS.items()}))`, &want)
	require.NotEmpty(t, want)

	for name, w := range want {
		got, known := patch.Cheats[name]
		require.Truef(t, known, "%s is a cheat the Python has and this does not", name)
		require.Equalf(t, w.Label, got.Label, "%s is labelled differently", name)
		require.Equalf(t, w.Note, got.Note, "%s says something different", name)
		require.Equalf(t, w.Anchor, got.Anchor, "%s is anchored elsewhere", name)
		require.Equalf(t, w.PatchOff, got.PatchOff, "%s patches a different offset", name)
		require.Equalf(t, w.Orig, hexOf(got.Orig), "%s expects different bytes there", name)
		require.Equalf(t, w.Patched, hexOf(got.Patched), "%s writes different bytes", name)
		require.Equalf(t, w.Tunable, got.Tunable(), "%s disagrees about being tunable", name)
		require.Equalf(t, w.ValueOff, got.ValueOff, "%s sets a different field", name)
		// The kind is only meaningful when there is a field to write. The
		// Python's default says "f32" for a cheat that sets nothing, which
		// describes nothing; carrying that across would be copying a value
		// rather than a fact.
		if w.ValueOff != 0 {
			require.Equalf(t, w.Kind == "f32", got.ValueF32, "%s field is a different type", name)
		}
		require.InDeltaf(t, w.OnValue, got.OnValue, 0, "%s turns on differently", name)
		require.InDeltaf(t, w.OffValue, got.OffValue, 0, "%s turns off differently", name)

		// The bytes written have to be the same length as the bytes replaced,
		// or the instruction after them starts mid-way through one.
		if !got.Tunable() {
			require.Lenf(t, got.Patched, len(got.Orig), "%s changes the length of the site", name)
		}
	}
	for name := range patch.Cheats {
		_, known := want[name]
		require.Truef(t, known, "%s is a cheat this has and the Python does not", name)
	}
}

/*
The bytes a tunable cheat writes are the same for every value it may be given.

These go into a running game as instructions. The whole declared range is swept,
plus the edges outside it, because a value a caller should not have sent is still
a value that can arrive.
*/
func TestTunableCheatsMatchThePython(t *testing.T) {
	values := []int32{-1, 0, 1, 2, 4, 10, 127, 128, 255, 1000}

	var want map[string][]string
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import patcher as P
print(json.dumps({n: [c.make_patched(v).hex() for v in %s]
                  for n, c in P.CHEATS.items() if c.make_patched}))`, pyI32(values)), &want)
	require.NotEmpty(t, want)

	for name, w := range want {
		got := patch.Cheats[name]
		require.Truef(t, got.Tunable(), "%s is tunable there and not here", name)
		for i, v := range values {
			require.Equalf(t, w[i], hexOf(got.MakePatched(v)),
				"%s writes different bytes for %d", name, v)
		}
	}
}

/*
The instruction fragments the stubs are assembled from are identical.

Every one of these is a few bytes of hand-written x86, and a mistake is not a
compile error: the game runs the wrong instructions and dies somewhere with no
connection to the trainer.
*/
func TestTheAssembledFragmentsMatchThePython(t *testing.T) {
	// Around the encoding boundaries as well as the ordinary range: an immediate
	// that fits a byte is encoded shorter, and getting that wrong changes the
	// length of the stub.
	values := []int32{-129, -128, -1, 0, 1, 2, 50, 100, 127, 128, 255, 1000, 100000}

	for _, c := range []struct {
		name   string
		python string
		build  func(int32) []byte
	}{
		{"force_xy", "P._force_xy", patch.ForceXY},
		{"imul_eax", "P._imul_eax", patch.ImulEAX},
		{"force_spawn", "P._force_spawn", patch.ForceSpawn},
		{"clamp_vanity_slot", "P._clamp_vanity_slot", patch.ClampVanitySlot},
		{"shrink_smart_cursor", "P._shrink_smart_cursor", patch.ShrinkSmartCursor},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want []string
			askPython(t, fmt.Sprintf(`
import json
from terrariabonker import patcher as P
print(json.dumps([%s(v).hex() for v in %s]))`, c.python, pyI32(values)), &want)

			for i, v := range values {
				require.Equalf(t, want[i], hexOf(c.build(v)),
					"%s assembles differently for %d", c.name, v)
			}
		})
	}
}

/*
The drop-chance clamp is compared over its declared range only.

A percentage of zero divides by zero, which the Python raises on. This returns
the smallest percentage there is instead, and that difference is deliberate and
outside the range the value can hold -- so it is stated here rather than compared.
*/
func TestTheDropChanceClampMatchesThePython(t *testing.T) {
	// Past 100 as well as inside the range. A percentage above 100 gives a
	// denominator of zero before it is floored, and a denominator of zero is a
	// division the game performs -- so the floor is not cosmetic, and both
	// implementations apply it.
	values := []int32{1, 2, 3, 25, 33, 50, 51, 99, 100, 101, 200, 1000}

	var want []string
	askPython(t, fmt.Sprintf(`
import json
from terrariabonker import patcher as P
print(json.dumps([P._cap_drop_denom(v).hex() for v in %s]))`, pyI32(values)), &want)

	for i, v := range values {
		require.Equalf(t, want[i], hexOf(patch.CapDropDenom(v)),
			"the drop floor assembles differently for %d", v)
	}
	require.Equal(t, hexOf(patch.CapDropDenom(1)), hexOf(patch.CapDropDenom(0)),
		"a percentage of zero is not treated as the smallest one")
}

// Every tunable is the same tunable: the same range, the same default, the same
// presets, and it belongs to the same cheat.
func TestEveryValueSpecMatchesThePython(t *testing.T) {
	var want map[string]struct {
		Kind    string   `json:"kind"`
		Default float64  `json:"default"`
		Lo      float64  `json:"lo"`
		Hi      float64  `json:"hi"`
		Unit    string   `json:"unit"`
		Presets [][2]any `json:"presets"`
	}
	askPython(t, `
import json
from terrariabonker import patcher as P
print(json.dumps({n: {
    "kind": s.kind, "default": float(s.default), "lo": float(s.lo), "hi": float(s.hi),
    "unit": s.unit,
    "presets": [[label, float(v)] for label, v in (s.presets or ())],
} for n, s in P._VALUE_SPECS.items()}))`, &want)
	require.NotEmpty(t, want)

	for name, w := range want {
		got, known := patch.ValueSpecs[name]
		require.Truef(t, known, "%s has a value there and not here", name)
		require.Equalf(t, w.Kind == "f32", got.F32, "%s is a different kind of number", name)
		require.InDeltaf(t, w.Default, got.Default, 0, "%s defaults differently", name)
		require.InDeltaf(t, w.Lo, got.Lo, 0, "%s starts elsewhere", name)
		require.InDeltaf(t, w.Hi, got.Hi, 0, "%s ends elsewhere", name)
		require.Equalf(t, w.Unit, got.Unit, "%s is described differently", name)
		require.Lenf(t, got.Presets, len(w.Presets), "%s offers a different number of choices", name)
		for i, p := range w.Presets {
			require.Equalf(t, p[0], got.Presets[i].Label, "%s choice %d is named differently", name, i)
			require.InDeltaf(t, p[1], got.Presets[i].Value, 0, "%s choice %d is a different value", name, i)
		}
	}
	for name := range patch.ValueSpecs {
		_, known := want[name]
		require.Truef(t, known, "%s has a value here and not there", name)
	}
}

/*
Every injection's table entry is the same, and in the same order of business.

The displaced bytes are what disabling writes back. Getting them wrong leaves a
method that is neither patched nor original, which runs until it does not.
*/
func TestEveryInjectionMatchesThePython(t *testing.T) {
	var want map[string]struct {
		Label      string `json:"label"`
		Anchor     string `json:"anchor"`
		InjectOff  int    `json:"inject_off"`
		Overwrite  string `json:"overwrite"`
		Rerun      bool   `json:"rerun"`
		Multi      bool   `json:"multi"`
		CallAnchor string `json:"call_anchor"`
		CallOff    int    `json:"call_target_off"`
		WritesCave bool   `json:"writes_cave"`
		Arena      bool   `json:"arena"`
		Note       string `json:"note"`
		Edits      []struct {
			Anchor  string `json:"anchor"`
			Off     int    `json:"off"`
			Orig    string `json:"orig"`
			Patched string `json:"patched"`
		} `json:"edits"`
	}
	askPython(t, `
import json
from terrariabonker import patcher as P
print(json.dumps({n: {
    "label": i.label, "anchor": i.anchor, "inject_off": i.inject_off,
    "overwrite": i.overwrite.hex(), "rerun": i.rerun_overwrite, "multi": i.multi,
    "call_anchor": i.call_anchor or "", "call_target_off": i.call_target_off,
    "writes_cave": i.writes_cave, "arena": i.arena, "note": i.note,
    "edits": [{"anchor": e.anchor, "off": e.off, "orig": e.orig.hex(),
               "patched": e.patched.hex()} for e in i.edits],
} for n, i in P.INJECTIONS.items()}))`, &want)
	require.NotEmpty(t, want)

	for name, w := range want {
		got, known := patch.Injections[name]
		require.Truef(t, known, "%s is an injection the Python has and this does not", name)
		require.Equalf(t, w.Label, got.Label, "%s is labelled differently", name)
		require.Equalf(t, w.Note, got.Note, "%s says something different", name)
		require.Equalf(t, w.Anchor, got.Anchor, "%s is anchored elsewhere", name)
		require.Equalf(t, w.InjectOff, got.InjectOff, "%s hooks a different offset", name)
		require.Equalf(t, w.Overwrite, hexOf(got.Overwrite), "%s displaces different bytes", name)
		require.Equalf(t, w.Rerun, got.RerunOverwrite, "%s disagrees about re-running them", name)
		require.Equalf(t, w.Multi, got.Multi, "%s disagrees about patching twins", name)
		require.Equalf(t, w.CallAnchor, got.CallAnchor, "%s calls through a different anchor", name)
		require.Equalf(t, w.CallOff, got.CallTargetOff, "%s finds that entry elsewhere", name)
		require.Equalf(t, w.WritesCave, got.WritesCave, "%s disagrees about writing in its cave", name)
		require.Equalf(t, w.Arena, got.Arena, "%s disagrees about needing the arena", name)

		require.Lenf(t, got.Edits, len(w.Edits), "%s carries a different number of edits", name)
		for i, e := range w.Edits {
			require.Equalf(t, e.Anchor, got.Edits[i].Anchor, "%s edit %d is elsewhere", name, i)
			require.Equalf(t, e.Off, got.Edits[i].Off, "%s edit %d is at a different offset", name, i)
			require.Equalf(t, e.Orig, hexOf(got.Edits[i].Orig), "%s edit %d expects different bytes", name, i)
			require.Equalf(t, e.Patched, hexOf(got.Edits[i].Patched), "%s edit %d writes different bytes", name, i)
			require.Lenf(t, got.Edits[i].Patched, len(got.Edits[i].Orig),
				"%s edit %d changes the length of the site", name, i)
		}

		// Every injection has to have exactly one way of building its stub.
		builders := 0
		if got.MakeBody != nil {
			builders++
		}
		if got.BuildBody != nil {
			builders++
		}
		if got.CallAnchor != "" {
			builders++
		}
		require.LessOrEqualf(t, builders, 1, "%s has more than one way to build its stub", name)
	}
	for name := range patch.Injections {
		_, known := want[name]
		require.Truef(t, known, "%s is an injection this has and the Python does not", name)
	}
}

/*
The catalog the window is given lists the same patches in the same order.

Order is what the window shows, and a cheat that fell out of the sections would
still be listed -- at the end -- rather than disappearing, which is the point of
the fallback.
*/
func TestTheCatalogMatchesThePython(t *testing.T) {
	var want []map[string]any
	askPython(t, `
import json
from terrariabonker import patcher as P
print(json.dumps([{"name": i.name, "label": i.label, "note": i.note,
                   "kind": i.kind, "section": i.section,
                   "value": i.value is not None}
                  for i in P.PATCH_CATALOG.values()]))`, &want)

	got := patch.Catalog()
	require.Len(t, got, len(want), "a different number of patches is offered")
	for i, w := range want {
		require.Equalf(t, w["name"], got[i].Name, "patch %d is a different one", i)
		require.Equalf(t, w["label"], got[i].Label, "%s is labelled differently", got[i].Name)
		require.Equalf(t, w["note"], got[i].Note, "%s says something different", got[i].Name)
		require.Equalf(t, w["kind"], got[i].Kind, "%s is a different kind", got[i].Name)
		require.Equalf(t, w["section"], got[i].Section, "%s is grouped elsewhere", got[i].Name)
		require.Equalf(t, w["value"], got[i].Value != nil, "%s disagrees about being tunable", got[i].Name)
	}
}

// A cheat nobody put in a section is still offered, at the end.
func TestAnUngroupedCheatFallsIntoTheLastSection(t *testing.T) {
	var want string
	askPython(t, `
import json
from terrariabonker import patcher as P
print(json.dumps(P._section_of("nothing_like_this")))`, &want)

	require.Equal(t, want, patch.SectionOf("nothing_like_this"))
	require.Equal(t, patch.Sections[len(patch.Sections)-1].Name, patch.SectionOf("nothing_like_this"))
}
