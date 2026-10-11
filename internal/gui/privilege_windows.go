package gui

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
