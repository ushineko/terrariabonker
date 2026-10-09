package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // a test's temp file
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// Two opens of one file stand in for two processes: flock is per open file
// description and LockFileEx per handle, so each excludes the other as a
// second process would.
func TestASecondHolderIsRefusedUntilTheFirstReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, second := open(t, path), open(t, path)

	release, err := Lock(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(second); !errors.Is(err, ErrHeld) {
		t.Fatalf("second TryLock while held: %v, want ErrHeld", err)
	}
	release()
	again, err := TryLock(second)
	if err != nil {
		t.Fatalf("second TryLock after release: %v", err)
	}
	again()
}

// Closing the file drops the lock, which is what a crashed holder amounts to.
func TestClosingTheFileDropsTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, second := open(t, path), open(t, path)
	if _, err := Lock(first); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	release, err := TryLock(second)
	if err != nil {
		t.Fatalf("TryLock after the holder closed: %v", err)
	}
	release()
}

// The holder's own write to the file can be read by someone else while the lock
// is held. The GUI names the holding pid this way.
func TestTheContentsStayReadableWhileLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	f := open(t, path)
	release, err := Lock(f)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := f.WriteAt([]byte("1234"), 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // a test's temp file
	if err != nil || string(got) != "1234" {
		t.Fatalf("read while locked: %q, %v", got, err)
	}
}
