package service

/*
What the package's own tests reach in that its callers do not.

Only the give-back hook: everything else about the caches is observable from
outside, and a path handed back to the user is not -- the call does nothing
unless the process is root, and the failure worth catching is nothing calling it
at all.
*/

// WatchGiveBack replaces the hand-back with a recorder and returns what it
// collects, restoring the real one when the test ends.
func WatchGiveBack(t interface{ Cleanup(func()) }) *[]string {
	was := giveBack
	t.Cleanup(func() { giveBack = was })
	var seen []string
	giveBack = func(path string) { seen = append(seen, path) }
	return &seen
}
