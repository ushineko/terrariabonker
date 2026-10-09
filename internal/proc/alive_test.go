package proc_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/proc"
)

/*
Alive is true for a running process and false once it has exited.

The child is this test binary with no tests selected, so it exists on every
platform and exits at once. On Windows the exit code is what decides it: the
process object outlives the process while a handle is open, so "can it be
opened" would answer yes for a while after it died.
*/
func TestAliveTellsARunningProcessFromAnExitedOne(t *testing.T) {
	require.True(t, proc.Alive(os.Getpid()), "this process is not alive")

	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$") //nolint:gosec // this test binary
	require.NoError(t, child.Start())
	pid := child.Process.Pid
	require.NoError(t, child.Wait())
	require.False(t, proc.Alive(pid), "an exited child still reads as alive")
}
