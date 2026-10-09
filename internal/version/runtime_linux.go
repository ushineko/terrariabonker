package version

import (
	"fmt"
	"os"
	"regexp"
)

// runtimeRe finds the runtime's version in the paths the process maps.
var runtimeRe = regexp.MustCompile(`wine-mono-([0-9][0-9.]*[0-9])`)

/*
DetectRuntime is the .NET runtime executing the game.

Worth knowing because the code patches match machine code that runtime's
compiler *emitted*, not anything in the game's own executable. The same game
under a different runtime compiles to different bytes, so a Proton update can
break a cheat with the game untouched -- and without this, that would look like
the game had changed.

Read from the module paths the process maps, which carry the version. The files
themselves live inside Proton's container and cannot be read from outside it.
*/
func DetectRuntime(pid int) string {
	maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	for _, m := range runtimeRe.FindAllSubmatch(maps, -1) {
		found[string(m[1])] = true
	}
	if len(found) == 0 {
		return ""
	}
	lowest := ""
	for v := range found {
		if lowest == "" || v < lowest {
			lowest = v
		}
	}
	return "wine-mono-" + lowest
}
