/*
Package filelock is an exclusive advisory lock on an open file, held until it is
released or the file is closed.

Four places took one with syscall.Flock, which does not exist on Windows. They
now share this: flock on Linux, LockFileEx on Windows. Both are dropped by the
system when the holder dies, which is what lets a crashed panel or worker never
leave a stale lock behind.

The Windows lock covers one byte far past the end of any real file rather than
the file's contents. LockFileEx locks are mandatory for the range they cover, so
locking the contents would stop a second process reading the holder's pid out
of the GUI's lock file -- which is how the "already open" message names it.
*/
package filelock

import (
	"errors"
	"os"
)

// ErrHeld is TryLock's answer when another holder has the lock.
var ErrHeld = errors.New("the lock is held by another process")

// Lock waits for the exclusive lock on f and returns its release.
func Lock(f *os.File) (func(), error) { return lock(f, true) }

// TryLock takes the exclusive lock on f if it is free, and returns ErrHeld if it
// is not.
func TryLock(f *os.File) (func(), error) { return lock(f, false) }
