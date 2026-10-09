package version

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
DetectRuntime is the .NET runtime executing the game: on Windows, .NET Framework,
named by the file version of the clr.dll the process loaded.

The file version and not the Framework's marketing number, because what the
code patches match is the output of that build's JIT, and a servicing update
changes the build without changing "4.8.1". Spec 052 keys version support on
this string.
*/
func DetectRuntime(pid int) string {
	for _, path := range proc.ModulePaths(pid) {
		if strings.EqualFold(filepath.Base(path), "clr.dll") {
			if v := fileVersion(path); v != "" {
				return "netfx-" + v
			}
		}
	}
	return ""
}

// fileVersion is a PE file's fixed file version as a.b.c.d, or "".
func fileVersion(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	block := unsafe.Pointer(&buf[0]) //nolint:gosec // the version API reads and writes a raw buffer
	if windows.GetFileVersionInfo(path, 0, size, block) != nil {
		return ""
	}
	var fixed *windows.VS_FIXEDFILEINFO
	var n uint32
	//nolint:gosec // VerQueryValue hands back a pointer into the buffer above
	if windows.VerQueryValue(block, `\`, unsafe.Pointer(&fixed), &n) != nil || fixed == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d",
		fixed.FileVersionMS>>16, fixed.FileVersionMS&0xFFFF,
		fixed.FileVersionLS>>16, fixed.FileVersionLS&0xFFFF)
}
