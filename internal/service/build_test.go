package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
An incompatible build can be forced past, and an unreadable one is not stopped at
all.

Forcing is the maintainer's override for a build the offsets have not been
re-derived for. Not stopping on an unreadable one is the more important half: the
version is unreadable for the first seconds of a launch, and a gate that refused
then would block the auto-restore that runs exactly then.
*/
func TestForcingAndTheUnreadableBuild(t *testing.T) {
	mem := plant()
	plantVersionInto(mem, "1.5.0.0") // a build the offsets do not fit
	svc := service.New(mem, -1)

	require.Error(t, svc.RequireCompatible(false), "an incompatible build was not refused")
	require.NoError(t, svc.RequireCompatible(true), "forcing did not get past the gate")

	blank := service.New(plant(), -1) // nothing readable
	level, _ := blank.Compatibility()
	require.Equal(t, version.Unknown, level)
	require.NoError(t, blank.RequireCompatible(false),
		"a version that cannot be read yet was treated as a wrong one")
}

/*
A build that reads as real is worked out once; one that does not is looked at
again.

Caching an unknown reading would pin the service at "incompatible" for its whole
life and wedge every write behind the gate -- restore included -- because the
runtime's own version can outnumber the game's before the game has loaded its
string.
*/
func TestOnlyARealBuildIsRemembered(t *testing.T) {
	mem := plant()
	svc := service.New(mem, -1)

	level, _ := svc.Compatibility()
	require.Equal(t, version.Unknown, level, "a version was read from a game that has none")

	// The game finishes loading and its version appears.
	plantVersionInto(mem, version.KnownVersion)
	level, _ = svc.Compatibility()
	require.Equal(t, version.Exact, level, "the unknown reading was cached and never revisited")

	// Now it is known, it stays known: the string going away does not un-know it.
	plantVersionInto(mem, "")
	level, _ = svc.Compatibility()
	require.Equal(t, version.Exact, level, "a settled build was worked out again")

	// Unless the caller says the world has changed under it.
	svc.Invalidate()
	level, _ = svc.Compatibility()
	require.Equal(t, version.Unknown, level, "invalidating did not drop the build")
}

// orEmpty is a value the Python may have written as null, as this spells it.
func orEmpty(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}

// plantVersionInto writes the version where the game keeps it, twice, which is
// what makes it the running one rather than a constant.
func plantVersionInto(mem *execMem, v string) {
	blank := make([]byte, 0x100)
	mem.PokeBytes(versionAt, blank)
	if v == "" {
		return
	}
	for i := range uint32(2) {
		mem.PokeBytes(versionAt+i*0x80, utf16le("v"+v))
	}
}

// versionAt is somewhere clear of everything else the fixture plants.
const versionAt = base + 0x600

// utf16le is a string as the game stores it.
func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}
