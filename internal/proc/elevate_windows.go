package proc

/*
Elevate does nothing on Windows.

A same-user process can open the game with read, write and query access, so
there is no privilege to acquire, and running the CLI as administrator would
only put files it writes out of the user's reach. The GUI still reaches memory
through the CLI, which keeps one code path and one JSON contract on both
platforms; it just does not need sudo to do it.
*/
func Elevate() error { return nil }
