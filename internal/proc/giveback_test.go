package proc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
Who ends up owning the caches the privileged side writes.

None of this can be exercised for real without being root, and being root is
exactly what makes it matter -- so the three system calls are answered by the
test, and what is checked is the decision: was a chown attempted, on what, and
for whom.

Every case was agreed with the implementation this was ported from, while both
existed. The paths are built under a scratch directory so the rule is what is
being tested rather than where the test ran.
*/

// recordChowns answers the system calls and collects every chown attempted.
func recordChowns(t *testing.T, uid int, home string, fail bool) *[]string {
	t.Helper()
	var calls []string
	wasEUID, wasChown, wasLookup := euid, chown, lookupID
	t.Cleanup(func() { euid, chown, lookupID = wasEUID, wasChown, wasLookup })
	euid = func() int { return uid }
	chown = func(path string, u, g int) error {
		if fail {
			return errors.New("read-only file system")
		}
		calls = append(calls, fmt.Sprintf("%s %d %d", path, u, g))
		return nil
	}
	lookupID = func(string) (string, error) {
		if home == "" {
			return "", errors.New("no such user")
		}
		return home, nil
	}
	return &calls
}

/*
The cases, as the one table both implementations are asked about.

`home` is the home directory the invoking user's account says they have, and
`path` the thing that was written; both are made absolute against a scratch
directory before either implementation is asked, so the answer is about the rule
rather than about where the test ran.
*/
type giveBackCase struct {
	name    string
	euid    int
	sudoUID string
	sudoGID string
	home    string // relative to the scratch directory, or "" for no such user
	path    string // relative to the scratch directory, or absolute if it starts with /
	exists  bool   // whether to create the file first
	symlink string // if set, `home` is a symlink to this
	want    bool   // whether the path should be handed over
}

var giveBackCases = []giveBackCase{
	{name: "an unprivileged run never chowns", euid: 1000, sudoUID: "1000",
		sudoGID: "1000", home: "u", path: "u/.cache/f.json", exists: true, want: false},
	{name: "root under sudo hands the file back", euid: 0, sudoUID: "1000",
		sudoGID: "1001", home: "u", path: "u/.cache/f.json", exists: true, want: true},
	{name: "a cache file not written yet is still judged", euid: 0, sudoUID: "1000",
		sudoGID: "1001", home: "u", path: "u/.cache/f.json", want: true},
	{name: "the home directory itself is handed back", euid: 0, sudoUID: "1000",
		sudoGID: "1001", home: "u", path: "u", exists: true, want: true},
	{name: "a path outside the user's home is left alone", euid: 0, sudoUID: "1000",
		sudoGID: "1001", home: "u", path: "/root/.cache/f.json", want: false},
	{name: "a sibling that only starts the same way is left alone", euid: 0,
		sudoUID: "1000", sudoGID: "1001", home: "u", path: "user-elsewhere/f.json",
		want: false},
	{name: "a real root shell is left alone", euid: 0, sudoUID: "", sudoGID: "",
		home: "u", path: "u/.cache/f.json", exists: true, want: false},
	{name: "a junk SUDO_UID is ignored", euid: 0, sudoUID: "not-a-number",
		sudoGID: "1001", home: "u", path: "u/.cache/f.json", exists: true, want: false},
	{name: "a missing SUDO_GID is ignored", euid: 0, sudoUID: "1000",
		sudoGID: "", home: "u", path: "u/.cache/f.json", exists: true, want: false},
	{name: "an account with no home is left alone", euid: 0, sudoUID: "1000",
		sudoGID: "1001", home: "", path: "u/.cache/f.json", exists: true, want: false},
	{
		/*
			A home reached through a symlink is still that home.

			/home -> /var/home is how several distributions ship, and comparing
			the paths as written makes every cache under it look like somebody
			else's.
		*/
		name: "a symlinked home is followed", euid: 0, sudoUID: "1000", sudoGID: "1001",
		home: "u", symlink: "real", path: "u/.cache/f.json", exists: true, want: true,
	},
}

// build lays the case out in a scratch directory and returns the two absolute
// paths.
func (c giveBackCase) build(t *testing.T) (home, path string) {
	t.Helper()
	root := t.TempDir()
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if strings.HasPrefix(p, "/") {
			return p
		}
		return filepath.Join(root, p)
	}
	home, path = abs(c.home), abs(c.path)
	if c.symlink != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(root, c.symlink), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(root, c.symlink), home))
	} else if home != "" {
		require.NoError(t, os.MkdirAll(home, 0o755))
	}
	if c.exists {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		if path != home {
			require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
		}
	}
	return home, path
}

func TestGivingBack(t *testing.T) {
	for _, c := range giveBackCases {
		t.Run(c.name, func(t *testing.T) {
			home, path := c.build(t)
			calls := recordChowns(t, c.euid, home, false)
			t.Setenv("SUDO_UID", c.sudoUID)
			t.Setenv("SUDO_GID", c.sudoGID)
			if c.sudoUID == "" {
				require.NoError(t, os.Unsetenv("SUDO_UID"))
			}
			if c.sudoGID == "" {
				require.NoError(t, os.Unsetenv("SUDO_GID"))
			}
			GiveBackToUser(path)

			want := []string{}
			if c.want {
				want = []string{fmt.Sprintf("%s 1000 1001", path)}
			}
			require.Equal(t, want, append([]string{}, *calls...),
				"a different decision about who owns it")
		})
	}
}

/*
A chown that fails is not an error.

A cache that cannot be handed over is still a usable cache, and the alternative
-- failing the scan that produced it -- would lose work over a permission the
caller cannot do anything about.
*/
func TestAChownFailureIsNotAnError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.json"), []byte("{}"), 0o600))
	recordChowns(t, 0, root, true)
	t.Setenv("SUDO_UID", "1000")
	t.Setenv("SUDO_GID", "1001")

	require.NotPanics(t, func() { GiveBackToUser(filepath.Join(root, "f.json")) })
}
