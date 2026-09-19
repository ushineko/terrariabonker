package projectile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/projectile"
)

/*
Holding overrides on what is in flight.

Nothing about this is a template edit: the game builds a projectile from its own
defaults every time it spawns one, so the values have to be written onto the
live object and rewritten onto the next one. A sweep that missed a slot is a
shot that behaves normally, which is indistinguishable from the cheat being off.
*/
func TestASweepWritesTheOverrides(t *testing.T) {
	mem := plantFlying()
	editor := projectile.NewEditor()

	got := editor.Sweep(mem, arr, overrides)
	require.Positive(t, got.Patched, "a sweep over live projectiles wrote nothing")
	require.NotEmpty(t, got.Types, "it reported nothing in flight")

	// The types it touched are the ones the overrides name, and nothing else.
	for id := range got.Types {
		require.Containsf(t, overrides, id, "%d was edited and is not overridden", id)
	}

	/*
		A second sweep over the same projectiles writes fewer fields.

		Most are rewritten every sweep on purpose -- the game resets them -- but
		the once-only ones are not, because writing those repeatedly is what made
		a projectile behave differently the longer it stayed in flight.
	*/
	again := editor.Sweep(mem, arr, overrides)
	require.Less(t, again.Patched, got.Patched,
		"a second sweep rewrote the once-only fields")
	require.Positive(t, again.Patched, "a second sweep stopped holding the values at all")
}

/*
Forgetting what was seen makes the next sweep write again.

That is what switching the cheat off and on has to do: the objects still hold
the old values and nothing else will put the new ones there.
*/
func TestForgettingMakesTheNextSweepWrite(t *testing.T) {
	mem := plantFlying()
	editor := projectile.NewEditor()

	first := editor.Sweep(mem, arr, overrides)
	require.Positive(t, first.Patched)
	require.Less(t, editor.Sweep(mem, arr, overrides).Patched, first.Patched)

	editor.Forget()
	require.Equal(t, first.Patched, editor.Sweep(mem, arr, overrides).Patched,
		"a forgotten editor did not write the values again")
}

// A value outside a field's range is clamped rather than written, because the
// game's own field is a byte or a small int and wrapping it is worse than
// refusing.
func TestClamping(t *testing.T) {
	for name, c := range map[string]struct{ given, want float64 }{
		"scale":        {given: 1000, want: projectile.Fields["scale"].Hi},
		"extraUpdates": {given: -5, want: projectile.Fields["extraUpdates"].Lo},
		"tileCollide":  {given: 7, want: projectile.Fields["tileCollide"].Hi},
	} {
		require.Equalf(t, c.want, projectile.Fields[name].Clamp(c.given),
			"%s was not clamped", name)
	}
	// And a value inside the range is left exactly as it was.
	require.InDelta(t, 2.5, projectile.Fields["scale"].Clamp(2.5), 0)
}
