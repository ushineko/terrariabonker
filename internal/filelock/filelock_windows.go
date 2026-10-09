//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockedByte is the one byte the lock covers: past the end of any file this
// program writes, so the contents stay readable to everyone.
const lockedByte = 0x7FFFFFFF

func lock(f *os.File, wait bool) (func(), error) {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	h := windows.Handle(f.Fd())
	region := func() *windows.Overlapped { return &windows.Overlapped{Offset: lockedByte} }
	if err := windows.LockFileEx(h, flags, 0, 1, 0, region()); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return func() {}, ErrHeld
		}
		return func() {}, fmt.Errorf("LockFileEx %s: %w", f.Name(), err)
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, region()) }, nil
}
