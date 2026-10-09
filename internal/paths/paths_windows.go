package paths

import (
	"os"
	"path/filepath"
)

// Config under %APPDATA% (roaming), cache under %LOCALAPPDATA%: a cache of
// icons decoded from the local install has no business following the user to
// another machine.
func configDir() (string, bool) { return under(os.UserConfigDir) }
func cacheDir() (string, bool)  { return under(os.UserCacheDir) }

func under(root func() (string, error)) (string, bool) {
	dir, err := root()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, App), true
}
