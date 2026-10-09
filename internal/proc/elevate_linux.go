package proc

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

/*
Elevate re-execs this program under sudo when it is not already root.

ptrace_scope=1 means a non-root process cannot open another process's
/proc/<pid>/mem, so anything that touches game memory has to run as root. This
replaces the current process, so it never returns when elevation happens.

`sudo -E` keeps the environment: an interactive prompt behaves, HOME still
points at the invoking user's home so caches land in the right place, and a
NOPASSWD sudoers entry makes it seamless.

The GUI never calls this. It runs unprivileged by design and reaches memory only
by shelling out to this program, which elevates on its own -- putting root
inside a process with a window in it is the thing that architecture exists to
avoid.
*/
func Elevate() error {
	if euid() == 0 {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this executable: %w", err)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("find sudo: %w", err)
	}
	args := append([]string{"sudo", "-E", self}, os.Args[1:]...)
	//nolint:gosec // sudo is resolved through PATH and re-runs this same binary
	if err := syscall.Exec(sudo, args, os.Environ()); err != nil {
		return fmt.Errorf("re-run under sudo: %w", err)
	}
	return nil
}
