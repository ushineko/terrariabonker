package buildinfo

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
The installer says the version the binaries inside it say.

The binaries are stamped from .tag by the Makefile. The installer's version is
app.version in fynstall.yaml, which fynstall reads as a literal: it names the
output files, and it is what Settings > Apps and a later upgrade compare. Two
files and one number, so a release that bumps .tag alone ships an installer
that calls itself the release before and offers to "repair" where it upgrades.
*/
func TestTheInstallerCarriesTheReleaseVersion(t *testing.T) {
	tag, err := os.ReadFile("../../.tag")
	require.NoError(t, err)
	want := strings.TrimPrefix(strings.TrimSpace(string(tag)), "v")
	require.NotEmpty(t, want, ".tag holds no version")

	manifest, err := os.ReadFile("../../fynstall.yaml")
	require.NoError(t, err)
	// The one `version:` key in the file is app's; a second would be a new
	// fynstall key this test has to learn about, and it says so.
	found := regexp.MustCompile(`(?m)^\s+version:\s*(\S+)`).FindAllStringSubmatch(string(manifest), -1)
	require.Len(t, found, 1, "fynstall.yaml should hold exactly one version key")
	require.Equal(t, want, found[0][1], "fynstall.yaml's app.version is not .tag's")
}
