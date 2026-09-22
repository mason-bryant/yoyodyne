//go:build !(darwin || linux)

package gitworktree

// loadAverage is unread on this platform, so the local Git budget is the idle
// figure and nothing scales it.
func loadAverage() (float64, bool) {
	return 0, false
}
