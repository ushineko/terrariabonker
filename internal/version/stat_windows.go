package version

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

/*
stampOf is what a file was when it was read: file index, modification time and
size -- the Windows counterpart of the inode stamp, enough to notice the file
being replaced.
*/
func stampOf(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return fmt.Sprintf("%d:%d:%d", fileIndex(path), info.ModTime().UnixNano(), info.Size()), nil
}

// fileIndex is the file's NTFS file index, or 0 when it cannot be read.
func fileIndex(path string) uint64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var fi windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &fi) != nil {
		return 0
	}
	return uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow)
}

/*
mappingIsCurrent is true on Windows: the path a running process reports is the
image it was loaded from.

Windows refuses to open a running image for writing, so the in-place
replacement the Linux inode check guards against cannot happen while the game
runs. A rename-and-replace could, and is not detected -- the same gap the Linux
check leaves for a file replaced and then put back. Steam does not update a
game that is running.
*/
func mappingIsCurrent(int, string, os.FileInfo) bool { return true }
