/*
Package paths is where this program keeps its files: one config directory, one
cache directory.

Eight places spelled these out by hand as home + ".config/terrariabonker" or
home + ".cache/terrariabonker". They ask here instead, so the Windows build can
put them where Windows keeps such things without eight platform switches.

On Linux the answers are exactly the old spellings, deliberately not
os.UserConfigDir: that honours XDG_CONFIG_HOME, which would move an existing
user's state the day this shipped, and under `sudo -E` the CLI and the
unprivileged window would have to agree on it too. HOME is what both already
agree on.

Each caller keeps its own fallback for "no home directory", because they differ
and that difference is not this package's to remove.
*/
package paths

// App is the directory name under the config and cache roots.
const App = "terrariabonker"

// ConfigDir is the config directory, and whether a home was found to put it in.
func ConfigDir() (string, bool) { return configDir() }

// CacheDir is the cache directory, and whether a home was found to put it in.
func CacheDir() (string, bool) { return cacheDir() }
