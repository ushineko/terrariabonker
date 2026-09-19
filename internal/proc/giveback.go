package proc

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

/*
Handing back what the privileged side wrote.

The memory work needs root, and the CLI gets there by re-execing under `sudo -E`
-- which keeps HOME pointing at the *user's* home. So a cache written by the
privileged side lands in their own directory owned by root, where they cannot
clear it without sudo, inside a directory that stays user-writable. The second
half of that is the worse half: root would be reading back a file an
unprivileged process could have replaced.
*/

/*
The three calls this reaches the system through, named so a test can answer them.

Every one of them is about the *running* process rather than about the file, so
none can be exercised by pointing the function at a scratch directory: a test
that wanted the real ones would have to be root.
*/
var (
	euid     = os.Geteuid
	chown    = os.Chown
	lookupID = func(uid string) (string, error) {
		u, err := user.LookupId(uid)
		if err != nil {
			return "", err
		}
		return u.HomeDir, nil
	}
)

/*
GiveBackToUser hands a file or directory this process wrote as root back to the
user who invoked it.

Only paths inside that user's own home are handed over. Without `sudo -E` HOME is
root's and the cache lands under /root instead -- and handing *that* over would
widen write access to a path inside root's home rather than fix anything.

Best effort by design: a cache that cannot be chowned is still a usable cache,
so every failure here is swallowed.
*/
func GiveBackToUser(path string) {
	if euid() != 0 {
		return
	}
	uid, err := strconv.Atoi(os.Getenv("SUDO_UID"))
	if err != nil {
		return // not under sudo (a real root shell); leave it alone
	}
	gid, err := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err != nil {
		return
	}
	home, err := lookupID(strconv.Itoa(uid))
	if err != nil {
		return
	}
	home = realPath(home)
	real := realPath(path)
	if real != home && !strings.HasPrefix(real, home+string(os.PathSeparator)) {
		return
	}
	_ = chown(path, uid, gid)
}

/*
realPath is the path with its symlinks followed, including when it does not
exist yet.

Go's EvalSymlinks refuses a path that is not there, and the path being judged
here is often a cache file about to be created. Python's os.path.realpath
resolves what it can and keeps the rest, so this does the same: walk up to the
deepest ancestor that exists, resolve that, and put the remainder back. Without
it, a home directory reached through a symlink -- /home -> /var/home, which is
how several distributions ship -- reads as outside itself and the cache is never
handed over.
*/
func realPath(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for at := path; ; {
		if resolved, err := filepath.EvalSymlinks(at); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(at)
		if parent == at {
			return path
		}
		rest = filepath.Join(filepath.Base(at), rest)
		at = parent
	}
}

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
		return err
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return err
	}
	args := append([]string{"sudo", "-E", self}, os.Args[1:]...)
	return syscall.Exec(sudo, args, os.Environ())
}
