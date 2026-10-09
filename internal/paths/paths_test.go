package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

/*
The directories are where each platform's previous code, or convention, put
them.

On Linux these are the literal spellings the eight call sites used before this
package existed, so a user's existing state is found where it already is. The
expected values are written out rather than built from App, so a change to App
fails here instead of quietly moving everyone's files.
*/
func TestTheDirectoriesAreWhereTheyWere(t *testing.T) {
	base := t.TempDir()
	var wantConfig, wantCache string
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(base, "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(base, "Local"))
		wantConfig = filepath.Join(base, "Roaming", "terrariabonker")
		wantCache = filepath.Join(base, "Local", "terrariabonker")
	} else {
		t.Setenv("HOME", base)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "elsewhere"))
		t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "elsewhere"))
		wantConfig = filepath.Join(base, ".config", "terrariabonker")
		wantCache = filepath.Join(base, ".cache", "terrariabonker")
	}
	if got, ok := ConfigDir(); !ok || got != wantConfig {
		t.Errorf("ConfigDir = %q, %v; want %q", got, ok, wantConfig)
	}
	if got, ok := CacheDir(); !ok || got != wantCache {
		t.Errorf("CacheDir = %q, %v; want %q", got, ok, wantCache)
	}
}
