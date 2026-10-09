//go:build !windows

package paths

import (
	"os"
	"path/filepath"
)

func configDir() (string, bool) { return underHome(".config") }
func cacheDir() (string, bool)  { return underHome(".cache") }

func underHome(root string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, root, App), true
}
