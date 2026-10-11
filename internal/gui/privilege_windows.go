package gui

import (
	"os/exec"
	"syscall"
)

/*
On Windows the CLI opens the game process with OpenProcess under the user's own
token -- a single-player game the user is already running needs no elevation
(see cmd/winrecon, which opens it without any). So there is no sudo: the CLI is
run directly, and the privilege check always passes.
*/

// cliLauncher returns the program to run and the argv before the subcommand.
// On Windows that is the CLI itself, with no wrapper.
func cliLauncher(cli string) (string, []string) {
	return cli, nil
}

// privilegeReady is always true on Windows: no elevation step stands between
// the window and a working CLI run.
func privilegeReady() bool { return true }

// createNoWindow is CREATE_NO_WINDOW: start a console program with no console.
const createNoWindow = 0x08000000

// hideConsole keeps Windows from giving the CLI its own console window. The GUI
// links as a GUI-subsystem binary, so it has no console of its own; without
// this, each console-subsystem CLI child (the serve worker, each one-shot run)
// gets a fresh console window -- the worker's would sit open for the whole
// session.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
