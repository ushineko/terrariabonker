package version

import (
	"fmt"
	"os"
	"syscall"
)

// inodeOf is a file's inode number, and whether the system said what it was.
func inodeOf(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}

/*
stampOf is what a file was when it was read: inode, modification time and size.

Enough to notice the file being replaced, which is the only thing the cache has
to be invalidated by.
*/
func stampOf(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	inode, _ := inodeOf(info)
	return fmt.Sprintf("%d:%d:%d", inode, info.ModTime().UnixNano(), info.Size()), nil
}
