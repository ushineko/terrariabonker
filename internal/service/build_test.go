package service_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/service"
	"github.com/ushineko/terrariabonker/internal/version"
)

/*
The build gate decides whether anything may write to the game.

Its two failure directions are not symmetric. Refusing a build that would have
worked costs the player their cheats; accepting one whose offsets have moved puts
numbers into the wrong fields of a live save. "I cannot tell yet" is not a third
kind of wrong -- it is recoverable, and it is what startup looks like.
*/
func TestTheBuildGateMatchesThePython(t *testing.T) {
	for _, c := range []struct {
		name    string
		planted string // the version string in the game's memory, twice over
	}{
		{"nothing readable", ""},
		{"the build this targets", version.KnownVersion},
		{"a fourth-component hotfix", "1.4.5.9"},
		{"a real update", "1.5.0.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var want map[string]any
			askPython(t, preamble()+plantVersion(c.planted)+`
level, msg = svc.compatibility()
snap = svc.snapshot(with_inventory=False)
try:
    svc.require_compatible()
    refused = False
except Exception:
    refused = True
print(json.dumps({"level": level, "msg": msg, "refused": refused,
                  "version": snap.version, "buildid": snap.buildid,
                  "compat_level": snap.compat_level}))`, &want)

			mem := plant()
			plantVersionInto(mem, c.planted)
			svc := service.New(mem, -1)

			level, msg := svc.Compatibility()
			require.Equal(t, want["level"], string(level), "a different verdict")
			require.Equal(t, want["msg"], msg, "a different reason")
			require.Equal(t, want["refused"], svc.RequireCompatible(false) != nil,
				"disagree about whether a write may go ahead")

			/*
				The Python spells "nothing" as null and this spells it as an empty
				string. That is a difference in spelling and not in meaning: the
				window's own type declares these as strings, and Go decodes a JSON
				null into exactly the empty string it would decode "" into. So
				what is compared is the value, not the way it is written.
			*/
			snap := svc.Snapshot(false)
			require.Equal(t, orEmpty(want["version"]), snap.Version, "a different version")
			require.Equal(t, orEmpty(want["buildid"]), snap.BuildID, "a different build id")
			require.Equal(t, want["compat_level"], string(snap.Level),
				"the snapshot reports a different verdict")
		})
	}
}

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

// plantVersion is the Python planting the same version string.
func plantVersion(v string) string {
	if v == "" {
		return ""
	}
	return fmt.Sprintf(`
for i in range(2):
    mem.poke_bytes(%d + i * 0x80, ("v%s").encode("utf-16-le"))
`, versionAt, v)
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
