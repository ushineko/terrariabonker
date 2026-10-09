//go:build !windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lock(f *os.File, wait bool) (func(), error) {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	fd := int(f.Fd()) //nolint:gosec // a file descriptor, which fits an int
	if err := syscall.Flock(fd, how); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return func() {}, ErrHeld
		}
		return func() {}, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }, nil
}
