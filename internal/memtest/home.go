package memtest

import (
	"path/filepath"
	"runtime"
	"testing"
)

/*
IsolateHome points every "this user's directory" lookup at home, so a test's
config and cache land in its temp directory and nowhere real.

Setting HOME alone was enough on Linux and is not on Windows, where
os.UserHomeDir reads USERPROFILE and the config and cache directories come from
APPDATA and LOCALAPPDATA. A test that set only HOME there wrote the developer's
real %APPDATA%\terrariabonker -- found the first time the suite ran on Windows,
by patch's TestTheTestsNeverTouchTheRealState. All four are set on every
platform: harmless where unused, and one call cannot be wrong on either.
*/
func IsolateHome(t testing.TB, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
}

/*
ConfigUnder and CacheUnder are where this program's config and cache are
expected to be once IsolateHome(home) has run.

Written out per platform rather than asked of internal/paths: a test that
checks a file landed in the right place by asking the code under test where the
right place is checks nothing. The Linux spellings are the ones every user's
existing state is in.
*/
func ConfigUnder(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "terrariabonker")
	}
	return filepath.Join(home, ".config", "terrariabonker")
}

// CacheUnder is ConfigUnder's counterpart for the cache.
func CacheUnder(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "terrariabonker")
	}
	return filepath.Join(home, ".cache", "terrariabonker")
}
