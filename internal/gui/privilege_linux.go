package gui

import (
	"context"
	"os/exec"
	"time"
)

/*
On Linux the CLI needs root to open the game's /proc/<pid>/mem, and the window
must never hold that privilege itself (see internal/proc). So every CLI run goes
through sudo, and the window first checks that sudo runs without a prompt.
*/

// cliLauncher returns the program to run and the argv before the subcommand.
// On Linux that is sudo with the CLI path.
//
// -n is non-interactive: without passwordless sudo the call fails at once with a
// clear message, where without it the process would block on a password prompt
// that has no terminal to appear on. -E keeps the environment, which is how the
// CLI finds the game the same way the user's shell would.
func cliLauncher(cli string) (string, []string) {
	return "sudo", []string{"-n", "-E", cli}
}

// privilegeReady reports whether sudo runs without a password, which is what
// every memory operation here needs. Bounded, because this runs before the first
// section is drawn and a sudo that hangs would hang the window's startup.
func privilegeReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), sudoProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

// sudoProbeTimeout bounds the passwordless-sudo check.
const sudoProbeTimeout = 5 * time.Second
