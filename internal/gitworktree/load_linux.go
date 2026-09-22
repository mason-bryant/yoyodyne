package gitworktree

import (
	"os"
	"strconv"
	"strings"
)

// loadAverage reads the one-minute load average, which is the first field of
// /proc/loadavg.
func loadAverage() (float64, bool) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, false
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return load, true
}
