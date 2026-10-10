package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/layout"
)

/*
Mono's in-place cheats are byte for byte what they were before they moved into
the version table.

On 2026-10-09 the four anchors and five in-place cheats moved from this package's
tables into layout's mono entry. Before the move, their patterns, ledgers, patch
offsets, original and patched bytes, the encoders' output for a spread of values,
and the player-field offsets were printed in this format and hashed. This
rebuilt the same text from the entry and got the same digest (commit 6f5fb2d):
the move changed where the numbers live and nothing else.

One deliberate change since: the ledgers dropped 1.4.5.7+24893155, a key that
named no real build (2026-10-09). The digest is of the data after that.
*/
func TestTheMonoCheatsAreWhatTheyWere(t *testing.T) {
	e := layout.Mono()
	var s string
	for _, k := range []string{"place", "pylon_place", "reset_block", "reset_minions"} {
		def := e.Anchors[k]
		s += fmt.Sprintf("%s|%s|%v|%v\n", k, MustParse(def.Pattern).String(), def.Unique, def.Verified)
	}
	for _, n := range []string{"fast_place", "max_minions", "mining", "pylons", "reach"} {
		site, c := e.Cheats[n], Cheats[n]
		s += fmt.Sprintf("%s|%s|%d|% X|% X", n, site.Anchor, site.PatchOff, site.Orig, site.Patched)
		if site.Encoder != "" {
			for _, v := range []int32{-3, 0, 1, 4, 10, 255} {
				s += fmt.Sprintf("|% X", encoders[site.Encoder](v))
			}
		}
		s += fmt.Sprintf("|%d|%v|%g|%g\n", e.PlayerValues[c.ValueField], c.ValueF32, c.OnValue, c.OffValue)
	}
	sum := sha256.Sum256([]byte(s))
	if got := hex.EncodeToString(sum[:]); got != "b3dadd0168e3bf14cb3613b2066e2c3a7566131415d04bc05c177a97bff29ce0" {
		t.Fatalf("the mono cheats changed in the move (digest %s):\n%s", got, s)
	}
}

// Every encoder a site names exists; an unknown name would panic at enable.
func TestEveryEncoderASiteNamesExists(t *testing.T) {
	for _, e := range layout.Entries {
		for name, site := range e.Cheats {
			if site.Encoder == "" {
				continue
			}
			if _, ok := encoders[site.Encoder]; !ok {
				t.Errorf("%s: %s names encoder %q, which does not exist", e.Name, name, site.Encoder)
			}
		}
	}
}

/*
Every build key in the version table and in every anchor's ledger is a declared
one (layout.SeenBuilds). A key spelt as a literal and misspelt matches nothing,
and nothing else would say so.
*/
func TestEveryBuildKeyIsADeclaredOne(t *testing.T) {
	declared := map[string]bool{}
	for _, b := range layout.SeenBuilds {
		declared[b] = true
	}
	check := func(where string, keys []string) {
		for _, k := range keys {
			if !declared[k] {
				t.Errorf("%s names build %q, which layout does not declare", where, k)
			}
		}
	}
	for _, e := range layout.Entries {
		check(e.Name+" builds", e.Builds)
		for key, a := range AnchorsFor(e) {
			check(e.Name+" anchor "+key, a.Verified)
			for _, v := range a.Variants {
				check(e.Name+" anchor "+key+" variant", []string{v.Build})
			}
		}
	}
}

// Every encoder writes exactly as many bytes as the site it is named by holds,
// for any value: more would overwrite the next instruction, fewer leave part of
// the old one to execute.
func TestEveryEncoderFillsItsSiteExactly(t *testing.T) {
	for _, e := range layout.Entries {
		for name, site := range e.Cheats {
			if site.Encoder == "" {
				require.Len(t, site.Patched, len(site.Orig), "%s: %s", e.Name, name)
				continue
			}
			for _, v := range []int32{-5, 0, 1, 4, 255, 1 << 20} {
				require.Len(t, encoders[site.Encoder](v), len(site.Orig), "%s: %s at %d", e.Name, name, v)
			}
		}
	}
}

// A site is recognised as the original, as the cheat's patch for any value, and
// as nothing else.
func TestASiteHoldsItsOriginalOrItsPatch(t *testing.T) {
	site := layout.Mono().Cheats["max_minions"]
	require.True(t, holds(site, site.Orig))
	require.True(t, holds(site, encoders[site.Encoder](37)))
	fixed := layout.Mono().Cheats["fast_place"]
	require.True(t, holds(fixed, encoders[fixed.Encoder](3)))
	require.False(t, holds(fixed, append([]byte{0xB9}, encoders[fixed.Encoder](3)[1:]...)), "another register")
	mining := layout.Mono().Cheats["mining"]
	require.True(t, holds(mining, mining.Patched))
	require.False(t, holds(mining, []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90}))
	require.False(t, holds(mining, mining.Orig[:4]), "short read")
}

/*
Mono's smart-cursor body is what it was before it was generalised over register
and offsets (digest of its output for eight values, recorded 2026-10-09 from the
code as it stood), and the CLR's differs from it only in register, offsets and
displaced bytes: two bytes longer for every value, with the CLR's at each end.
*/
func TestTheSmartCursorBodies(t *testing.T) {
	var s string
	for _, n := range []int32{-1, 0, 1, 3, 20, 127, 128, 100000} {
		s += fmt.Sprintf("% X\n", ShrinkSmartCursor(n))
		require.Len(t, netfxSmartCursor.shrink(n), len(ShrinkSmartCursor(n))+2, "%d", n)
	}
	sum := sha256.Sum256([]byte(s))
	require.Equal(t, "f4a7a5fead82514d40c4a9d39ee6bedcd929d40664f1a8e31dc84a39a16b2001", hex.EncodeToString(sum[:]))
	clr := netfxSmartCursor.shrink(4)
	require.Equal(t, []byte{0x89, 0x43, 0x20, 0x8B, 0x43, 0x14, 0x03, 0x43, 0x18}, clr[:9],
		"the store, then mov eax,[ebx+14]; add eax,[ebx+18]")
	require.Equal(t, []byte{0x83, 0x7D, 0xC4, 0x00}, clr[len(clr)-4:])
}

/*
The teleport stub scales the ping's tile coordinates to pixels, calls
Player.Teleport(newPos, 0, 0) with the player baked in ecx, restores esp and
reproduces the displaced prologue. No player (no world) is an error, not a silent
no-op.
*/
func TestTeleportCallBody(t *testing.T) {
	_, err := TeleportCallBody(0, 0x22F37E90, []byte{0x56})
	require.Error(t, err, "a teleport stub with no player")

	over := []byte{0x56, 0x83, 0xEC, 0x0C, 0x33, 0xC0}
	body, err := TeleportCallBody(0x10000800, 0x22F37E90, over)
	require.NoError(t, err)
	require.Equal(t, []byte{
		0x60,       // pushad
		0x8B, 0xDC, // mov ebx,esp
		0x8B, 0x45, 0x08, // mov eax,[ebp+08] -- ping tile X
		0x05, 0x00, 0x00, 0x00, 0x02, // add eax, x16
		0x8B, 0x4D, 0x0C, // mov ecx,[ebp+0C] -- ping tile Y
		0x81, 0xC1, 0x00, 0x00, 0x00, 0x02, // add ecx, x16
		0x51,       // push ecx -- newPos.Y
		0x50,       // push eax -- newPos.X
		0x6A, 0x00, // push 0 -- extraInfo
		0x33, 0xD2, // xor edx,edx -- Style 0
		0xB9, 0x00, 0x08, 0x00, 0x10, // mov ecx, playerObj
		0xB8, 0x90, 0x7E, 0xF3, 0x22, // mov eax, teleport
		0xFF, 0xD0, // call eax
		0x8B, 0xE3, // mov esp,ebx
		0x61,                               // popad
		0x56, 0x83, 0xEC, 0x0C, 0x33, 0xC0, // the displaced prologue
	}, body)
}
