package version

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

const clrPath = `C:\Windows\Microsoft.NET\Framework\v4.0.30319\clr.dll`

/*
The runtime is named by clr.dll, and a clr.dll whose version cannot be read
still names the family.

Falling back to "" there would read as "nothing detected", which the version
table lets through -- with numbers derived under mono. The family alone is
what refuses them, so it is reported even without a version.
*/
func TestTheRuntimeIsNamedByTheCLR(t *testing.T) {
	read := func(v string) func(string) string { return func(string) string { return v } }
	mods := []string{`C:\Games\Terraria\Terraria.exe`, `C:\WINDOWS\Microsoft.NET\Framework\v4.0.30319\CLR.DLL`}

	require.Equal(t, "netfx-4.8.9345.0", runtimeOf(mods, read("4.8.9345.0")))
	require.Equal(t, "netfx-unknown", runtimeOf(mods, read("")),
		"a CLR with an unreadable version read as no runtime at all")
	require.Equal(t, "", runtimeOf(mods[:1], read("4.8.9345.0")), "no CLR, but a runtime was reported")
}

// The version is read from the real file, in the shape the table keys on.
func TestTheCLRsFileVersionIsRead(t *testing.T) {
	if _, err := os.Stat(clrPath); err != nil {
		t.Skip(".NET Framework is not installed here")
	}
	require.Regexp(t, regexp.MustCompile(`^4\.\d+\.\d+\.\d+$`), fileVersion(clrPath))
}
